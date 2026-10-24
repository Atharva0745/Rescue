package intake

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rescue-gateway/gateway/pkg/source"
)

var ErrDuplicate = fmt.Errorf("duplicate report")
var ErrConflict = fmt.Errorf("conflicting payload hash")

// PayloadHash computes the stable digest used for receipt and idempotency checks.
func PayloadHash(payload []byte) string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}

// Report is the normalized intake record before durable write.
type Report struct {
	ID          string
	SourceKey   string
	Nonce       string
	PayloadHash string
	Payload     []byte
	CreatedAt   time.Time
}

// Receipt records the durable acceptance of a report.
type Receipt struct {
	ReportID       string
	SourceKey      string
	IdempotencyKey string
	PayloadHash    string
	CreatedAt      time.Time
	FsyncOK        bool
}

// Policy validates a candidate intake request against source quotas and sets
// the normalized source state that the controller will use for admission.
type Policy struct {
	Registry *source.Registry
	FsyncOK  bool
}

func NewPolicy(registry *source.Registry, fsyncOK bool) *Policy {
	return &Policy{Registry: registry, FsyncOK: fsyncOK}
}

func (p *Policy) ValidateRequest(sourceID, nonce, group, asn, region string, payload []byte) (Report, error) {
	if p == nil || p.Registry == nil {
		return Report{}, fmt.Errorf("intake policy registry is not configured")
	}
	if strings.TrimSpace(sourceID) == "" {
		sourceID = "anonymous"
	}
	if strings.TrimSpace(group) == "" {
		group = sourceID
	}
	if strings.TrimSpace(asn) == "" {
		asn = "unknown-asn"
	}
	if strings.TrimSpace(region) == "" {
		region = "unknown-region"
	}
	state := p.Registry.RegisterSource(sourceID, group, asn, region)
	if state == nil {
		return Report{}, fmt.Errorf("source %s could not be registered", sourceID)
	}
	if err := p.Registry.CheckAdmission(sourceID, 1); err != nil {
		return Report{}, err
	}
	return Report{
		ID:          fmt.Sprintf("report-%d", time.Now().UnixNano()),
		SourceKey:   sourceID,
		Nonce:       nonce,
		PayloadHash: PayloadHash(payload),
		Payload:     append([]byte(nil), payload...),
		CreatedAt:   time.Now(),
	}, nil
}

// IntakeLog stores receipt state and enforces idempotency semantics.
type IntakeLog struct {
	mu       sync.Mutex
	receipts map[string]Receipt
	byKey    map[string]string
	fsyncOK  bool
}

func NewIntakeLog(fsyncOK bool) *IntakeLog {
	return &IntakeLog{
		receipts: make(map[string]Receipt),
		byKey:    make(map[string]string),
		fsyncOK:  fsyncOK,
	}
}

// IdempotencyScope computes the stable scope under which a report is deduplicated.
func IdempotencyScope(sourceKey, nonce string) string {
	sourceKey = normalizeSourceKey(sourceKey)
	nonce = normalizeNonce(nonce)
	encoded := make([]byte, 0, 16+len(sourceKey)+len(nonce))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(sourceKey)))
	encoded = append(encoded, length[:]...)
	encoded = append(encoded, sourceKey...)
	binary.BigEndian.PutUint64(length[:], uint64(len(nonce)))
	encoded = append(encoded, length[:]...)
	encoded = append(encoded, nonce...)
	h := sha256.Sum256(encoded)
	return hex.EncodeToString(h[:])
}

// Accept ensures a report is durably recorded once. A repeated same key and same
// payload hash returns the prior receipt. A same key with conflicting payload is rejected.
func (l *IntakeLog) Accept(r Report) (Receipt, error) {
	r.SourceKey = normalizeSourceKey(r.SourceKey)
	r.Nonce = normalizeNonce(r.Nonce)
	payloadHash := PayloadHash(r.Payload)
	if r.PayloadHash != "" && r.PayloadHash != payloadHash {
		return Receipt{}, fmt.Errorf("payload hash does not match report payload")
	}
	r.PayloadHash = payloadHash
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	if strings.TrimSpace(r.ID) == "" {
		r.ID = fmt.Sprintf("report-%d", r.CreatedAt.UnixNano())
	}

	key := IdempotencyScope(r.SourceKey, r.Nonce)
	l.mu.Lock()
	defer l.mu.Unlock()

	if existing, ok := l.byKey[key]; ok {
		if rec, exists := l.receipts[existing]; exists {
			if rec.PayloadHash == r.PayloadHash {
				return rec, nil
			}
			return Receipt{}, fmt.Errorf("%w: key %s has conflicting payloads", ErrConflict, key)
		}
	}
	if !l.fsyncOK {
		return Receipt{}, fmt.Errorf("durable intake fsync failed; no new receipts issued")
	}
	if _, exists := l.receipts[r.ID]; exists {
		return Receipt{}, fmt.Errorf("report ID %s is already assigned to another receipt", r.ID)
	}

	rec := Receipt{
		ReportID:       r.ID,
		SourceKey:      r.SourceKey,
		IdempotencyKey: key,
		PayloadHash:    r.PayloadHash,
		CreatedAt:      r.CreatedAt,
		FsyncOK:        true,
	}
	l.receipts[rec.ReportID] = rec
	l.byKey[key] = rec.ReportID
	return rec, nil
}

func normalizeSourceKey(sourceKey string) string {
	if strings.TrimSpace(sourceKey) == "" {
		return "anonymous"
	}
	return sourceKey
}

func normalizeNonce(nonce string) string {
	if strings.TrimSpace(nonce) == "" {
		return "-"
	}
	return nonce
}

func (l *IntakeLog) ReceiptForKey(key string) (Receipt, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id, ok := l.byKey[key]
	if !ok {
		return Receipt{}, false
	}
	rec, ok := l.receipts[id]
	return rec, ok
}

func (l *IntakeLog) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.receipts)
}
