package readmetrics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Constants for file paths
const LogFilePath = "tmp/FIX.4.4-CUST2_Order-ANCHORAGE.messages.current.log"
const OutputFilePath = "tmp/log_data.json"

// LogEntry represents a structure for the relevant log information
type LogEntry struct {
	MessageType string            `json:"message_type"`
	Timestamp   string            `json:"timestamp"`
	Fields      map[string]string `json:"fields"`
}

// Struct to store log entries
type LogMetricsEntry struct {
	timestamp time.Time
	msgType   string
	clOrdID   string
}

// Execute reads the log file, extracts relevant information, and saves it as JSON
func Execute() error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error getting working directory: %v", err)
	}

	logFile, err := os.Open(filepath.Join(dir, LogFilePath))
	if err != nil {
		return fmt.Errorf("error opening log file: %v", err)
	}
	defer logFile.Close()

	scanner := bufio.NewScanner(logFile)
	entries := make([]LogEntry, 0)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "35=D") || strings.Contains(line, "35=8") {
			entry := LogEntry{
				Fields: make(map[string]string),
			}

			parts := strings.Split(line, " ")
			if len(parts) > 2 {
				entry.MessageType = strings.Split(parts[2], "\u0001")[0]
				entry.Timestamp = parts[1]

				// Extract fields
				for _, part := range parts {
					if strings.Contains(part, "=") {
						keyValue := strings.SplitN(part, "=", 2)
						if len(keyValue) == 2 {
							entry.Fields[keyValue[0]] = keyValue[1]
						}
					}
				}
			}

			entries = append(entries, entry)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading log file: %v", err)
	}

	if err := saveToJSON(entries); err != nil {
		return fmt.Errorf("error saving to JSON: %v", err)
	}

	if err := CalculateLatenciesToFile(LogFilePath); err != nil {
		return fmt.Errorf("error calculating latencies: %v", err)
	}

	fmt.Printf("Raw Data saved to %s\n", OutputFilePath)
	return nil
}

// saveToJSON converts entries to JSON format and saves to a file
func saveToJSON(entries []LogEntry) error {
	jsonData, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("error converting to JSON: %v", err)
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error getting working directory: %v", err)
	}
	outputFile, err := os.Create(filepath.Join(dir, OutputFilePath))
	if err != nil {
		return fmt.Errorf("error creating output file: %v", err)
	}
	defer outputFile.Close()

	_, err = outputFile.Write(jsonData)
	if err != nil {
		return fmt.Errorf("error writing to output file: %v", err)
	}

	return nil
}

// parseFIXMessage parses a FIX message from a log line.
func parseFIXMessage(line string) (LogMetricsEntry, error) {
	fields := strings.Split(line, "")
	msg := LogMetricsEntry{}
	timestampStr := line[:26]
	timestamp, err := time.Parse("2006/01/02 15:04:05.000000", timestampStr)
	if err != nil {
		return msg, err
	}
	msg.timestamp = timestamp

	for _, field := range fields {
		if strings.HasPrefix(field, "35=") {
			msg.msgType = strings.TrimPrefix(field, "35=")
		} else if strings.HasPrefix(field, "11=") {
			msg.clOrdID = strings.TrimPrefix(field, "11=")
		}
	}
	return msg, nil
}

// CalculateLatenciesToFile reads a log file, calculates latencies for 35=D messages,
// and writes the latencies and throughput to a file in the /tmp directory.
func CalculateLatenciesToFile(logFilePath string) error {
	file, err := os.Open(logFilePath)
	if err != nil {
		return fmt.Errorf("error opening file: %v", err)
	}
	defer file.Close()

	dMessages := make(map[string]LogMetricsEntry)
	latencies := []int64{} // Store latencies in an array for average calculation
	throughputCounts := make(map[time.Time]int)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		msg, err := parseFIXMessage(line)
		if err != nil {
			fmt.Println("Error parsing line:", err)
			continue
		}

		// Track 35=D message timestamps for latency and throughput
		if msg.msgType == "D" {
			dMessages[msg.clOrdID] = msg

			// Round down timestamp to the nearest minute for throughput calculation
			minute := msg.timestamp.Truncate(time.Minute)
			throughputCounts[minute]++
		} else if msg.msgType == "8" && msg.clOrdID != "" {
			// Calculate latency
			if dMsg, found := dMessages[msg.clOrdID]; found {
				latency := msg.timestamp.Sub(dMsg.timestamp).Milliseconds()
				latencies = append(latencies, latency)
				delete(dMessages, msg.clOrdID) // Remove to avoid multiple calculations for same ClOrdID
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file: %v", err)
	}

	// Write output to the log_metrics file
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error getting working directory: %v", err)
	}
	outputFile, err := os.Create(filepath.Join(dir, "tmp/log_metrics.txt"))
	if err != nil {
		return fmt.Errorf("error creating log file: %v", err)
	}
	defer outputFile.Close()

	writer := bufio.NewWriter(outputFile)

	// Write latency data
	for _, latency := range latencies {
		_, err := writer.WriteString(fmt.Sprintf("Latency: %d ms\n", latency))
		if err != nil {
			return fmt.Errorf("error writing to log file: %v", err)
		}
	}

	// Calculate average latency
	averageLatency := float64(0)
	if len(latencies) > 0 {
		for _, latency := range latencies {
			averageLatency += float64(latency)
		}
		averageLatency /= float64(len(latencies))
	}

	// Write the average latency to the log file
	_, err = writer.WriteString(fmt.Sprintf("Average Latency: %.2f ms\n", averageLatency))
	if err != nil {
		return fmt.Errorf("error writing average latency to log file: %v", err)
	}

	// Write throughput data
	for minute, count := range throughputCounts {
		throughputStr := fmt.Sprintf("Minute: %s, Throughput: %d orders/min\n", minute.Format("2006-01-02 15:04"), count)
		_, err := writer.WriteString(throughputStr)
		if err != nil {
			return fmt.Errorf("error writing throughput to log file: %v", err)
		}
	}

	writer.Flush()

	return nil
}