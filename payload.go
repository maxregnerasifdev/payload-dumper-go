package main

import (
    "bytes"
    "compress/bzip2"
    "crypto/sha256"
    "encoding/binary"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sort"
    "sync"

    humanize "github.com/dustin/go-humanize"
    "github.com/spencercw/go-xz"
    "github.com/valyala/gozstd"
    "github.com/vbauerster/mpb/v5"
    "github.com/vbauerster/mpb/v5/decor"
    "google.golang.org/protobuf/proto"
    "github.com/ssut/payload-dumper-go/chromeos_update_engine"
)

// PayloadPair represents a pair of source and target payload files
type PayloadPair struct {
    SourcePayload *Payload
    TargetPayload *Payload
    OutputDir     string
}

// Constants
const (
    payloadHeaderMagic        = "CrAU"
    brilloMajorPayloadVersion = 2
    blockSize                 = 4096
)

// Existing Payload and other type definitions remain the same...

// NewPayloadPair creates a new PayloadPair for processing
func NewPayloadPair(sourceFile, targetFile, outputDir string) (*PayloadPair, error) {
    sourcePay := NewPayload(sourceFile)
    targetPay := NewPayload(targetFile)

    return &PayloadPair{
        SourcePayload: sourcePay,
        TargetPayload: targetPay,
        OutputDir:     outputDir,
    }, nil
}

// ProcessPayloads handles the extraction and porting of both payloads
func (pp *PayloadPair) ProcessPayloads() error {
    // Initialize both payloads
    if err := pp.SourcePayload.Open(); err != nil {
        return fmt.Errorf("failed to open source payload: %v", err)
    }
    defer pp.SourcePayload.file.Close()

    if err := pp.TargetPayload.Open(); err != nil {
        return fmt.Errorf("failed to open target payload: %v", err)
    }
    defer pp.TargetPayload.file.Close()

    if err := pp.SourcePayload.Init(); err != nil {
        return fmt.Errorf("failed to initialize source payload: %v", err)
    }

    if err := pp.TargetPayload.Init(); err != nil {
        return fmt.Errorf("failed to initialize target payload: %v", err)
    }

    // Create output directory if it doesn't exist
    if err := os.MkdirAll(pp.OutputDir, 0755); err != nil {
        return fmt.Errorf("failed to create output directory: %v", err)
    }

    // Process both payloads
    return pp.extractAndPort()
}

func (pp *PayloadPair) extractAndPort() error {
    fmt.Println("Starting payload extraction and porting process...")

    // Create progress bars container
    progress := mpb.New()
    
    // Extract partitions from both payloads
    sourcePartitions := make(map[string]*chromeos_update_engine.PartitionUpdate)
    targetPartitions := make(map[string]*chromeos_update_engine.PartitionUpdate)

    for _, partition := range pp.SourcePayload.deltaArchiveManifest.Partitions {
        sourcePartitions[*partition.PartitionName] = partition
    }

    for _, partition := range pp.TargetPayload.deltaArchiveManifest.Partitions {
        targetPartitions[*partition.PartitionName] = partition
    }

    // Process each partition
    var wg sync.WaitGroup
    errorChan := make(chan error, len(targetPartitions))

    for name, targetPart := range targetPartitions {
        wg.Add(1)
        go func(partName string, partition *chromeos_update_engine.PartitionUpdate) {
            defer wg.Done()

            outputPath := filepath.Join(pp.OutputDir, fmt.Sprintf("%s.img", partName))
            outFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
            if err != nil {
                errorChan <- fmt.Errorf("failed to create output file for partition %s: %v", partName, err)
                return
            }
            defer outFile.Close()

            // Extract the partition
            if err := pp.TargetPayload.Extract(partition, outFile); err != nil {
                errorChan <- fmt.Errorf("failed to extract partition %s: %v", partName, err)
                return
            }

        }(name, targetPart)
    }

    // Wait for all goroutines to complete
    wg.Wait()
    close(errorChan)

    // Check for any errors
    for err := range errorChan {
        if err != nil {
            return err
        }
    }

    fmt.Println("Payload extraction and porting completed successfully!")
    return nil
}

func main() {
    if len(os.Args) != 4 {
        fmt.Println("Usage: payload_dumper <source_payload.bin> <target_payload.bin> <output_directory>")
        os.Exit(1)
    }

    sourceFile := os.Args[1]
    targetFile := os.Args[2]
    outputDir := os.Args[3]

    payloadPair, err := NewPayloadPair(sourceFile, targetFile, outputDir)
    if err != nil {
        fmt.Printf("Error creating payload pair: %v\n", err)
        os.Exit(1)
    }

    if err := payloadPair.ProcessPayloads(); err != nil {
        fmt.Printf("Error processing payloads: %v\n", err)
        os.Exit(1)
    }
}
