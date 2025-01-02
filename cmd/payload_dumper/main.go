package main

import (
    "archive/zip"
    "flag"
    "fmt"
    "io"
    "log"
    "os"
    "runtime"
    "strings"
    "time"
)

func extractPayloadBin(filename string) string {
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
            tempfile, err := os.CreateTemp(os.TempDir(), "payload_*.bin")
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

func main() {
    runtime.GOMAXPROCS(runtime.NumCPU())
    
    var (
        list            bool
        partitions      string
        outputDirectory string
        concurrency     int
    )

    flag.IntVar(&concurrency, "c", 4, "Number of multiple workers to extract (shorthand)")
    flag.IntVar(&concurrency, "concurrency", 4, "Number of multiple workers to extract")
    flag.BoolVar(&list, "l", false, "Show list of partitions in payload.bin (shorthand)")
    flag.BoolVar(&list, "list", false, "Show list of partitions in payload.bin")
    flag.StringVar(&outputDirectory, "o", "", "Set output directory (shorthand)")
    flag.StringVar(&outputDirectory, "output", "", "Set output directory")
    flag.StringVar(&partitions, "p", "", "Dump only selected partitions (comma-separated) (shorthand)")
    flag.StringVar(&partitions, "partitions", "", "Dump only selected partitions (comma-separated)")
    flag.Parse()

    if flag.NArg() == 0 {
        usage()
    }

    filename := flag.Arg(0)
    if _, err := os.Stat(filename); os.IsNotExist(err) {
        log.Fatalf("File does not exist: %s\n", filename)
    }

    payloadBin := filename
    if strings.HasSuffix(filename, ".zip") {
        fmt.Println("Please wait while extracting payload.bin from the archive.")
        payloadBin = extractPayloadBin(filename)
        if payloadBin == "" {
            log.Fatal("Failed to extract payload.bin from the archive.")
        } else {
            defer os.Remove(payloadBin)
        }
    }

    fmt.Printf("payload.bin: %s\n", payloadBin)
}

func usage() {
    fmt.Fprintf(os.Stderr, "Usage: %s [options] [inputfile]\n", os.Args[0])
    flag.PrintDefaults()
    os.Exit(2)
}
