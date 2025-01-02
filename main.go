package main

import (
    "archive/zip"
    "flag"
    "fmt"
    "io"
    "log"
    "os"
    "path/filepath"
    "runtime"
    "strings"
    "time"
)

type PayloadExtractor struct {
    sourceFile      string
    targetFile      string
    outputDirectory string
    concurrency     int
}

func extractPayloadBin(filename string, prefix string) string {
    zipReader, err := zip.OpenReader(filename)
    if err != nil {
        log.Fatalf("Not a valid zip archive: %s\n", filename)
    }
    defer zipReader.Close()

    for _, file := range zipReader.Reader.File {
        if file.Name == "payload.bin" && file.UncompressedSize64 > 0 {
            zippedFile, err := file.Open()
            if err != nil {
                log.Fatalf("Failed to read zipped file: %s\n", file.Name)
            }
            tempfile, err := os.CreateTemp(os.TempDir(), fmt.Sprintf("%s_payload_*.bin", prefix))
            if err != nil {
                log.Fatalf("Failed to create a temp file located at %s\n", tempfile.Name())
            }
            defer tempfile.Close()

            _, err = io.Copy(tempfile, zippedFile)
            if err != nil {
                log.Fatal(err)
            }
            return tempfile.Name()
        }
    }
    return ""
}

func NewPayloadExtractor() *PayloadExtractor {
    return &PayloadExtractor{
        concurrency: runtime.NumCPU(),
    }
}

func (pe *PayloadExtractor) processPayloads() error {
    // Process source payload
    sourcePayload := NewPayload(pe.sourceFile)
    if err := sourcePayload.Open(); err != nil {
        return fmt.Errorf("failed to open source payload: %v", err)
    }
    defer sourcePayload.file.Close()

    // Process target payload
    targetPayload := NewPayload(pe.targetFile)
    if err := targetPayload.Open(); err != nil {
        return fmt.Errorf("failed to open target payload: %v", err)
    }
    defer targetPayload.file.Close()

    // Initialize both payloads
    if err := sourcePayload.Init(); err != nil {
        return fmt.Errorf("failed to initialize source payload: %v", err)
    }
    if err := targetPayload.Init(); err != nil {
        return fmt.Errorf("failed to initialize target payload: %v", err)
    }

    // Set concurrency for both payloads
    sourcePayload.SetConcurrency(pe.concurrency)
    targetPayload.SetConcurrency(pe.concurrency)

    // Create output directories
    sourcePath := filepath.Join(pe.outputDirectory, "source")
    targetPath := filepath.Join(pe.outputDirectory, "target")

    for _, dir := range []string{sourcePath, targetPath} {
        if err := os.MkdirAll(dir, 0755); err != nil {
            return fmt.Errorf("failed to create directory %s: %v", dir, err)
        }
    }

    // Extract payloads
    fmt.Printf("Extracting source payload to: %s\n", sourcePath)
    if err := sourcePayload.ExtractAll(sourcePath); err != nil {
        return fmt.Errorf("failed to extract source payload: %v", err)
    }

    fmt.Printf("Extracting target payload to: %s\n", targetPath)
    if err := targetPayload.ExtractAll(targetPath); err != nil {
        return fmt.Errorf("failed to extract target payload: %v", err)
    }

    return nil
}

func main() {
    pe := NewPayloadExtractor()

    var (
        list       bool
        partitions string
    )

    // Define command line flags
    flag.StringVar(&pe.sourceFile, "source", "", "Source payload.bin or zip file")
    flag.StringVar(&pe.targetFile, "target", "", "Target payload.bin or zip file")
    flag.StringVar(&pe.outputDirectory, "o", "", "Output directory (shorthand)")
    flag.StringVar(&pe.outputDirectory, "output", "", "Output directory")
    flag.IntVar(&pe.concurrency, "c", runtime.NumCPU(), "Number of concurrent workers")
    flag.BoolVar(&list, "l", false, "List partitions only")
    flag.StringVar(&partitions, "p", "", "Extract specific partitions (comma-separated)")

    flag.Parse()

    // Validate required arguments
    if pe.sourceFile == "" || pe.targetFile == "" {
        fmt.Println("Both source and target files are required")
        usage()
    }

    // Create output directory if not specified
    if pe.outputDirectory == "" {
        now := time.Now()
        pe.outputDirectory = fmt.Sprintf("extracted_%d%02d%02d_%02d%02d%02d",
            now.Year(), now.Month(), now.Day(),
            now.Hour(), now.Minute(), now.Second())
    }

    // Handle zip files
    if strings.HasSuffix(pe.sourceFile, ".zip") {
        fmt.Println("Extracting payload.bin from source zip...")
        if extracted := extractPayloadBin(pe.sourceFile, "source"); extracted != "" {
            pe.sourceFile = extracted
            defer os.Remove(extracted)
        }
    }

    if strings.HasSuffix(pe.targetFile, ".zip") {
        fmt.Println("Extracting payload.bin from target zip...")
        if extracted := extractPayloadBin(pe.targetFile, "target"); extracted != "" {
            pe.targetFile = extracted
            defer os.Remove(extracted)
        }
    }

    // Process the payloads
    if err := pe.processPayloads(); err != nil {
        log.Fatalf("Error processing payloads: %v", err)
    }

    fmt.Println("Payload extraction completed successfully!")
}

func usage() {
    fmt.Fprintf(os.Stderr, "Usage: %s -source <source_file> -target <target_file> [options]\n", os.Args[0])
    fmt.Fprintf(os.Stderr, "\nOptions:\n")
    flag.PrintDefaults()
    os.Exit(2)
}
