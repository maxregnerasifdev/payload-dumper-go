package payload

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
    "sync"

    "github.com/dustin/go-humanize"
    "github.com/spencercw/go-xz"
    "github.com/valyala/gozstd"
    "github.com/vbauerster/mpb/v5"
    "github.com/vbauerster/mpb/v5/decor"
    "google.golang.org/protobuf/proto"
    "github.com/ssut/payload-dumper-go/chromeos_update_engine"
)

const (
    Version = "2.0.0"
    payloadHeaderMagic = "CrAU"
    brilloMajorPayloadVersion = 2
    blockSize = 4096
)

type Payload struct {
    Filename    string
    file       *os.File
    header     *payloadHeader
    deltaArchiveManifest *chromeos_update_engine.DeltaArchiveManifest
    signatures *chromeos_update_engine.Signatures
    concurrency int
    metadataSize int64
    dataOffset   int64
    initialized  bool
    requests    chan *request
    workerWG    sync.WaitGroup
    progress    *mpb.Progress
}

type payloadHeader struct {
    Version              uint64
    ManifestLen          uint64
    MetadataSignatureLen uint32
    Size                 uint64
    payload             *Payload
}

type request struct {
    partition       *chromeos_update_engine.PartitionUpdate
    targetDirectory string
}

type ProgressReporter struct {
    bar     *mpb.Bar
    total   int64
    current int64
}

func NewPayload(filename string) *Payload {
    return &Payload{
        Filename:    filename,
        concurrency: 4,
    }
}

func (p *Payload) SetConcurrency(n int) {
    p.concurrency = n
}

func (p *Payload) GetConcurrency() int {
    return p.concurrency
}

func (p *Payload) Open() error {
    file, err := os.Open(p.Filename)
    if err != nil {
        return err
    }
    p.file = file
    return nil
}

func (p *Payload) Close() error {
    if p.file != nil {
        return p.file.Close()
    }
    return nil
}

func (ph *payloadHeader) ReadFromPayload() error {
    buf := make([]byte, 4)
    if _, err := ph.payload.file.Read(buf); err != nil {
        return err
    }
    if string(buf) != payloadHeaderMagic {
        return fmt.Errorf("Invalid payload magic: %s", buf)
    }

    buf = make([]byte, 8)
    if _, err := ph.payload.file.Read(buf); err != nil {
        return err
    }
    ph.Version = binary.BigEndian.Uint64(buf)
    fmt.Printf("Payload Version: %d\n", ph.Version)

    if ph.Version != brilloMajorPayloadVersion {
        return fmt.Errorf("Unsupported payload version: %d", ph.Version)
    }

    buf = make([]byte, 8)
    if _, err := ph.payload.file.Read(buf); err != nil {
        return err
    }
    ph.ManifestLen = binary.BigEndian.Uint64(buf)
    fmt.Printf("Payload Manifest Length: %d\n", ph.ManifestLen)
    ph.Size = 24

    buf = make([]byte, 4)
    if _, err := ph.payload.file.Read(buf); err != nil {
        return err
    }
    ph.MetadataSignatureLen = binary.BigEndian.Uint32(buf)
    fmt.Printf("Payload Manifest Signature Length: %d\n", ph.MetadataSignatureLen)
    return nil
}

func (p *Payload) readManifest() (*chromeos_update_engine.DeltaArchiveManifest, error) {
    buf := make([]byte, p.header.ManifestLen)
    if _, err := p.file.Read(buf); err != nil {
        return nil, err
    }
    deltaArchiveManifest := &chromeos_update_engine.DeltaArchiveManifest{}
    if err := proto.Unmarshal(buf, deltaArchiveManifest); err != nil {
        return nil, err
    }
    return deltaArchiveManifest, nil
}

func (p *Payload) readMetadataSignature() (*chromeos_update_engine.Signatures, error) {
    if _, err := p.file.Seek(int64(p.header.Size+p.header.ManifestLen), 0); err != nil {
        return nil, err
    }
    buf := make([]byte, p.header.MetadataSignatureLen)
    if _, err := p.file.Read(buf); err != nil {
        return nil, err
    }
    signatures := &chromeos_update_engine.Signatures{}
    if err := proto.Unmarshal(buf, signatures); err != nil {
        return nil, err
    }
    return signatures, nil
}

func (p *Payload) Init() error {
    p.header = &payloadHeader{
        payload: p,
    }
    if err := p.header.ReadFromPayload(); err != nil {
        return err
    }

    deltaArchiveManifest, err := p.readManifest()
    if err != nil {
        return err
    }
    p.deltaArchiveManifest = deltaArchiveManifest

    signatures, err := p.readMetadataSignature()
    if err != nil {
        return err
    }
    p.signatures = signatures

    p.metadataSize = int64(p.header.Size + p.header.ManifestLen)
    p.dataOffset = p.metadataSize + int64(p.header.MetadataSignatureLen)

    fmt.Println("Found partitions:")
    for i, partition := range p.deltaArchiveManifest.Partitions {
        fmt.Printf("%s (%s)", partition.GetPartitionName(), humanize.Bytes(*partition.GetNewPartitionInfo().Size))
        if i < len(deltaArchiveManifest.Partitions)-1 {
            fmt.Printf(", ")
        } else {
            fmt.Printf("\n")
        }
    }

    p.initialized = true
    return nil
}

func (p *Payload) readDataBlob(offset int64, length int64) ([]byte, error) {
    buf := make([]byte, length)
    n, err := p.file.ReadAt(buf, p.dataOffset+offset)
    if err != nil {
        return nil, err
    }
    if int64(n) != length {
        return nil, fmt.Errorf("Read length mismatch: %d != %d", n, length)
    }
    return buf, nil
}

func (p *Payload) Extract(partition *chromeos_update_engine.PartitionUpdate, out *os.File) error {
    name := partition.GetPartitionName()
    info := partition.GetNewPartitionInfo()
    totalOperations := len(partition.Operations)
    barName := fmt.Sprintf("%s (%s)", name, humanize.Bytes(info.GetSize()))
    
    bar := p.progress.AddBar(
        int64(totalOperations),
        mpb.PrependDecorators(
            decor.Name(barName, decor.WCSyncSpaceR),
        ),
        mpb.AppendDecorators(
            decor.Percentage(),
        ),
    )
    defer bar.SetTotal(0, true)

    for _, operation := range partition.Operations {
        if len(operation.DstExtents) == 0 {
            return fmt.Errorf("Invalid operation.DstExtents for the partition %s", name)
        }
        bar.Increment()

        e := operation.DstExtents[0]
        dataOffset := p.dataOffset + int64(operation.GetDataOffset())
        dataLength := int64(operation.GetDataLength())

        _, err := out.Seek(int64(e.GetStartBlock())*blockSize, 0)
        if err != nil {
            return err
        }

        expectedUncompressedBlockSize := int64(e.GetNumBlocks() * blockSize)
        bufSha := sha256.New()
        teeReader := io.TeeReader(io.NewSectionReader(p.file, dataOffset, dataLength), bufSha)

        switch operation.GetType() {
        case chromeos_update_engine.InstallOperation_REPLACE:
            n, err := io.Copy(out, teeReader)
            if err != nil {
                return err
            }
            if int64(n) != expectedUncompressedBlockSize {
                return fmt.Errorf("Verify failed (Unexpected bytes written): %s (%d != %d)", name, n, expectedUncompressedBlockSize)
            }
            break

        case chromeos_update_engine.InstallOperation_REPLACE_XZ:
            reader := xz.NewDecompressionReader(teeReader)
            n, err := io.Copy(out, &reader)
            if err != nil {
                return err
            }
            reader.Close()
            if n != expectedUncompressedBlockSize {
                return fmt.Errorf("Verify failed (Unexpected bytes written): %s (%d != %d)", name, n, expectedUncompressedBlockSize)
            }
            break

        case chromeos_update_engine.InstallOperation_REPLACE_BZ:
            reader := bzip2.NewReader(teeReader)
            n, err := io.Copy(out, reader)
            if err != nil {
                return err
            }
            if n != expectedUncompressedBlockSize {
                return fmt.Errorf("Verify failed (Unexpected bytes written): %s (%d != %d)", name, n, expectedUncompressedBlockSize)
            }
            break

        case chromeos_update_engine.InstallOperation_ZERO:
            reader := bytes.NewReader(make([]byte, expectedUncompressedBlockSize))
            n, err := io.Copy(out, reader)
            if err != nil {
                return err
            }
            if n != expectedUncompressedBlockSize {
                return fmt.Errorf("Verify failed (Unexpected bytes written): %s (%d != %d)", name, n, expectedUncompressedBlockSize)
            }
            break

        case chromeos_update_engine.InstallOperation_ZSTD:
            reader := gozstd.NewReader(teeReader)
            n, err := io.Copy(out, reader)
            if err != nil {
                return err
            }
            if n != expectedUncompressedBlockSize {
                return fmt.Errorf("Verify failed (Unexpected bytes written): %s (%d != %d)", name, n, expectedUncompressedBlockSize)
            }
            break

        default:
            return fmt.Errorf("Unhandled operation type: %s", operation.GetType().String())
        }

        hash := hex.EncodeToString(bufSha.Sum(nil))
        expectedHash := hex.EncodeToString(operation.GetDataSha256Hash())
        if expectedHash != "" && hash != expectedHash {
            return fmt.Errorf("Verify failed (Checksum mismatch): %s (%s != %s)", name, hash, expectedHash)
        }
    }
    return nil
}

func (p *Payload) worker() {
    for req := range p.requests {
        partition := req.partition
        targetDirectory := req.targetDirectory
        name := fmt.Sprintf("%s.img", partition.GetPartitionName())
        filepath := fmt.Sprintf("%s/%s", targetDirectory, name)
        
        file, err := os.OpenFile(filepath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0o755)
        if err != nil {
            fmt.Println(err.Error())
            continue
        }

        if err := p.Extract(partition, file); err != nil {
            fmt.Println(err.Error())
        }
        
        file.Close()
        p.workerWG.Done()
    }
}

func (p *Payload) spawnExtractWorkers(n int) {
    for i := 0; i < n; i++ {
        go p.worker()
    }
}

func (p *Payload) ExtractSelected(targetDirectory string, partitions []string) error {
    if !p.initialized {
        return errors.New("Payload has not been initialized")
    }

    p.progress = mpb.New()
    p.requests = make(chan *request, 100)
    p.spawnExtractWorkers(p.concurrency)

    for _, partition := range p.deltaArchiveManifest.Partitions {
        if len(partitions) > 0 {
            found := false
            for _, name := range partitions {
                if name == partition.GetPartitionName() {
                    found = true
                    break
                }
            }
            if !found {
                continue
            }
        }

        p.workerWG.Add(1)
        p.requests <- &request{
            partition:       partition,
            targetDirectory: targetDirectory,
        }
    }

    p.workerWG.Wait()
    close(p.requests)
    return nil
}

func (p *Payload) ExtractAll(targetDirectory string) error {
    return p.ExtractSelected(targetDirectory, nil)
}

func PrintVersionInfo() {
    fmt.Printf("Payload Dumper Go v%s\n", Version)
    fmt.Printf("Block Size: %d bytes\n", blockSize)
}

func (p *Payload) PrintInfo() {
    fmt.Printf("\nPayload Information:\n")
    fmt.Printf("File: %s\n", p.Filename)
    fmt.Printf("Version: %d\n", p.header.Version)
    fmt.Printf("Manifest Length: %d bytes\n", p.header.ManifestLen)
    fmt.Printf("Metadata Signature Length: %d bytes\n", p.header.MetadataSignatureLen)
    fmt.Printf("Metadata Size: %d bytes\n", p.metadataSize)
    fmt.Printf("Data Offset: %d bytes\n", p.dataOffset)
    
    if p.deltaArchiveManifest != nil {
        fmt.Printf("\nPartitions:\n")
        for _, partition := range p.deltaArchiveManifest.Partitions {
            fmt.Printf("- %s (%s)\n", 
                partition.GetPartitionName(),
                humanize.Bytes(partition.GetNewPartitionInfo().GetSize()))
        }
    }
}

func (p *Payload) VerifyPayload() error {
    if !p.initialized {
        return errors.New("payload not initialized")
    }

    fmt.Println("\nVerifying payload...")
    
    // Verify magic
    buf := make([]byte, 4)
    if _, err := p.file.ReadAt(buf, 0); err != nil {
        return err
    }
    if string(buf) != payloadHeaderMagic {
        return fmt.Errorf("invalid magic: %s", string(buf))
    }

    // Verify version
    if p.header.Version != brilloMajorPayloadVersion {
        return fmt.Errorf("unsupported version: %d", p.header.Version)
    }

    // Verify manifest
    if p.deltaArchiveManifest == nil {
        return errors.New("manifest not loaded")
    
