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

    "github.com/ssut/payload-dumper-go/pkg/payload"
    "github.com/ssut/payload-dumper-go/pkg/extractor"
)

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

func processPayload(payloadPath string, outputDir string, list bool, partitions string, concurrency int) error {
    p := payload.NewPayload(payloadPath)
    if err := p.Open(); err != nil {
        return fmt.Errorf("failed to open payload: %v", err)
    }
    defer p.Close()

    if err := p.Init(); err != nil {
        return fmt.Errorf("failed to initialize payload: %v", err)
    }

    if list {
        p.PrintInfo()
        return nil
    }

    if err := p.VerifyPayload(); err != nil {
        return fmt.Errorf("payload verification failed: %v", err)
    }

    p.SetConcurrency(concurrency)
    fmt.Printf("Number of workers: %d\n", p.GetConcurrency())

    if partitions != "" {
        return p.ExtractSelected(outputDir, strings.Split(partitions, ","))
    }
    return p.ExtractAll(outputDir)
}

func main() {
    runtime.GOMAXPROCS(runtime.NumCPU())

    var (
        sourceFile      string
        targetFile      string
        outputDirectory string
        list           bool
        partitions     string
        concurrency    int
        version        bool
    )

    flag.StringVar(&sourceFile, "source", "", "Source payload.bin or zip file")
    flag.StringVar(&targetFile, "target", "", "Target payload.bin or zip file (optional)")
    flag.StringVar(&outputDirectory, "o", "", "Output directory")
    flag.StringVar(&outputDirectory, "output", "", "Output directory")
    flag.BoolVar(&list, "l", false, "List partitions only")
    flag.BoolVar(&list, "list", false, "List partitions only")
    flag.StringVar(&partitions, "p", "", "Extract specific partitions (comma-separated)")
    flag.StringVar(&partitions, "partitions", "", "Extract specific partitions (comma-separated)")
    flag.IntVar(&concurrency, "c", runtime.NumCPU(), "Number of concurrent workers")
    flag.IntVar(&concurrency, "concurrency", runtime.NumCPU(), "Number of concurrent workers")
    flag.BoolVar(&version, "v", false, "Show version information")
    flag.BoolVar(&version, "version", false, "Show version information")

    flag.Parse()

    if version {
        payload.PrintVersionInfo()
        return
    }

    if sourceFile == "" && flag.NArg() > 0 {
        sourceFile = flag.Arg(0)
    }

    if sourceFile == "" {
        fmt.Fprintf(os.Stderr, "Usage: %s [options] <input_file>\n", os.Args[0])
        flag.PrintDefaults()
        os.Exit(2)
    }

    // Verify source file exists
    if _, err := os.Stat(sourceFile); os.IsNotExist(err) {
        log.Fatalf("Source file does not exist: %s\n", sourceFile)
    }

    // Set output directory if not specified
    if outputDirectory == "" {
        now := time.Now()
        outputDirectory = fmt.Sprintf("extracted_%d%02d%02d_%02d%02d%02d",
            now.Year(), now.Month(), now.Day(),
            now.Hour(), now.Minute(), now.Second())
    }

    // Create output directory
    if err := os.MkdirAll(outputDirectory, 0755); err != nil {
        log.Fatalf("Failed to create output directory: %v\n", err)
    }

    // Handle zip files
    sourcePayloadBin := sourceFile
    if strings.HasSuffix(sourceFile, ".zip") {
        fmt.Println("Extracting payload.bin from source archive...")
        sourcePayloadBin = extractPayloadBin(sourceFile, "source")
        if sourcePayloadBin == "" {
            log.Fatal("Failed to extract payload.bin from source archive")
        }
        defer os.Remove(sourcePayloadBin)
    }

    // Handle dual payload mode
    if targetFile != "" {
        targetPayloadBin := targetFile
        if strings.HasSuffix(targetFile, ".zip") {
            fmt.Println("Extracting payload.bin from target archive...")
            targetPayloadBin = extractPayloadBin(targetFile, "target")
            if targetPayloadBin == "" {
                log.Fatal("Failed to extract payload.bin from target archive")
            }
            defer os.Remove(targetPayloadBin)
        }

        ext := extractor.NewPayloadExtractor()
        ext.SetSourceFile(sourcePayloadBin)
        ext.SetTargetFile(targetPayloadBin)
        ext.SetOutputDirectory(outputDirectory)
        ext.SetConcurrency(concurrency)

        if err := ext.Process(); err != nil {
            log.Fatalf("Error processing payloads: %v", err)
        }
    } else {
        // Single payload mode
        if err := processPayload(sourcePayloadBin, outputDirectory, list, partitions, concurrency); err != nil {
            log.Fatal(err)
        }
    }

    fmt.Printf("\nExtraction completed successfully!\n")
    fmt.Printf("Output directory: %s\n", outputDirectory)
}
