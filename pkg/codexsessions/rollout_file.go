package codexsessions

import (
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// rolloutFile retains the on-disk stat while streaming decompressed JSONL.
// Do not decompress to disk: archived rollouts can be very large.
type rolloutFile struct {
	*os.File
	decoder *zstd.Decoder
}

func openRolloutFile(path string) (*rolloutFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rollout := &rolloutFile{File: file}
	if strings.HasSuffix(path, ".jsonl.zst") {
		rollout.decoder, err = zstd.NewReader(file, zstd.WithDecoderConcurrency(1))
		if err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	return rollout, nil
}

func (f *rolloutFile) Read(p []byte) (int, error) {
	if f.decoder != nil {
		return f.decoder.Read(p)
	}
	return f.File.Read(p)
}

func (f *rolloutFile) Close() error {
	if f.decoder != nil {
		f.decoder.Close()
	}
	return f.File.Close()
}
