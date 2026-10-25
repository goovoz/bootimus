package catalogue

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
	"time"
)

type VerifyProgress struct {
	Filename  string    `json:"filename"`
	Status    string    `json:"status"`
	Expected  string    `json:"expected"`
	Actual    string    `json:"actual,omitempty"`
	Verified  bool      `json:"verified"`
	Percent   float64   `json:"percent"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

type Verifier struct {
	mu   sync.Mutex
	jobs map[string]*VerifyProgress
}

func NewVerifier() *Verifier {
	return &Verifier{jobs: make(map[string]*VerifyProgress)}
}

func (v *Verifier) Get(filename string) *VerifyProgress {
	v.mu.Lock()
	defer v.mu.Unlock()
	if p, ok := v.jobs[filename]; ok {
		copied := *p
		return &copied
	}
	return nil
}

func (v *Verifier) Begin(filename, expected string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if p, ok := v.jobs[filename]; ok && p.Status == "running" {
		return false
	}
	v.jobs[filename] = &VerifyProgress{Filename: filename, Status: "running", Expected: expected, StartedAt: time.Now()}
	return true
}

func (v *Verifier) update(filename string, fn func(p *VerifyProgress)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if p, ok := v.jobs[filename]; ok {
		fn(p)
	}
}

func (v *Verifier) Run(filename, filePath, expected string, done func(actual string, verified bool)) {
	actual, err := HashFile(filePath, func(read, total int64) {
		v.update(filename, func(p *VerifyProgress) {
			if total > 0 {
				p.Percent = float64(read) / float64(total) * 100
			}
		})
	})
	if err != nil {
		v.update(filename, func(p *VerifyProgress) {
			p.Status = "error"
			p.Error = err.Error()
		})
		return
	}
	verified := actual == expected
	v.update(filename, func(p *VerifyProgress) {
		p.Status = "completed"
		p.Percent = 100
		p.Actual = actual
		p.Verified = verified
		if !verified {
			p.Error = "checksum mismatch"
		}
	})
	if done != nil {
		done(actual, verified)
	}
}

func HashFile(path string, progress func(read, total int64)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var read int64
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			read += int64(n)
			if progress != nil {
				progress(read, info.Size())
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
