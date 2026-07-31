package agentintake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

var ErrReceiptConflict = errors.New("agent submission receipt conflict")

type FileReceiptStore struct {
	Root   string
	Locker store.Locker
}

func NewFileReceiptStore(root string, locker store.Locker) *FileReceiptStore {
	return &FileReceiptStore{Root: root, Locker: locker}
}

func ReceiptID(kind SubmissionKind, raw []byte) string {
	payload := []byte(string(kind) + "\n" + RawSHA256(raw))
	digest := sha256.Sum256(payload)
	return "sub-" + hex.EncodeToString(digest[:12])
}

func RawSHA256(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			raw = canonical
		}
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (s *FileReceiptStore) LockProcessing(ctx context.Context, id string) (store.Lock, error) {
	if s.Locker == nil || !protocol.IsValidID(id) {
		return nil, fmt.Errorf("agent receipt store is not configured")
	}
	return s.Locker.Lock(ctx, filepath.Join(s.Root, "collaboration", ".runtime", "locks", "agent-submissions", id+".processing.lock"))
}

func (s *FileReceiptStore) SaveReceived(ctx context.Context, receipt Receipt) (Receipt, bool, error) {
	if err := receipt.validateReceived(); err != nil {
		return Receipt{}, false, err
	}
	lock, err := s.lock(ctx, receipt.ID)
	if err != nil {
		return Receipt{}, false, err
	}
	defer lock.Unlock()
	existing, err := s.read(receipt.ID)
	if err == nil {
		if existing.Kind == receipt.Kind && existing.RawSHA256 == receipt.RawSHA256 {
			return existing, false, nil
		}
		return Receipt{}, false, ErrReceiptConflict
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Receipt{}, false, err
	}
	if err := s.write(receipt); err != nil {
		return Receipt{}, false, err
	}
	return receipt, true, nil
}

func (s *FileReceiptStore) Finalize(ctx context.Context, receipt Receipt) (Receipt, error) {
	if err := receipt.validateFinal(); err != nil {
		return Receipt{}, err
	}
	lock, err := s.lock(ctx, receipt.ID)
	if err != nil {
		return Receipt{}, err
	}
	defer lock.Unlock()
	existing, err := s.read(receipt.ID)
	if err != nil {
		return Receipt{}, err
	}
	if existing.Kind != receipt.Kind {
		return Receipt{}, fmt.Errorf("%w: kind differs", ErrReceiptConflict)
	}
	if existing.RawSHA256 != receipt.RawSHA256 {
		return Receipt{}, fmt.Errorf("%w: raw payload differs", ErrReceiptConflict)
	}
	if existing.Status != Received {
		if sameReceiptOutcome(existing, receipt) {
			return existing, nil
		}
		return Receipt{}, ErrReceiptConflict
	}
	receipt.ReceivedAt = existing.ReceivedAt
	if err := s.write(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (s *FileReceiptStore) List(ctx context.Context) ([]Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.directory())
	if errors.Is(err, os.ErrNotExist) {
		return []Receipt{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Receipt, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("unsafe agent receipt entry %q", entry.Name())
		}
		receipt, err := s.read(stringsTrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		result = append(result, receipt)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].ReceivedAt.Equal(result[right].ReceivedAt) {
			return result[left].ID > result[right].ID
		}
		return result[left].ReceivedAt.After(result[right].ReceivedAt)
	})
	return result, nil
}

func (s *FileReceiptStore) read(id string) (Receipt, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, fmt.Errorf("decode agent receipt: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Receipt{}, fmt.Errorf("agent receipt contains multiple JSON values")
	}
	if receipt.ID != id {
		return Receipt{}, fmt.Errorf("agent receipt id does not match path")
	}
	if err := receipt.validateAny(); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (s *FileReceiptStore) write(receipt Receipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.directory(), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.directory(), ".receipt-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	writeErr := error(nil)
	if count, err := file.Write(append(data, '\n')); err != nil {
		writeErr = err
	} else if count != len(data)+1 {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tempPath, s.path(receipt.ID))
}

func (s *FileReceiptStore) lock(ctx context.Context, id string) (store.Lock, error) {
	if s.Locker == nil || !protocol.IsValidID(id) {
		return nil, fmt.Errorf("agent receipt store is not configured")
	}
	return s.Locker.Lock(ctx, filepath.Join(s.Root, "collaboration", ".runtime", "locks", "agent-submissions", id+".lock"))
}

func (s *FileReceiptStore) directory() string {
	return filepath.Join(s.Root, "collaboration", ".runtime", "agent-submissions")
}

func (s *FileReceiptStore) path(id string) string {
	return filepath.Join(s.directory(), id+".json")
}

func (r Receipt) validateAny() error {
	if !protocol.IsValidID(r.ID) || (r.Kind != ResponseSubmission && r.Kind != TaskSubmission) || (r.Status != Received && r.Status != Accepted && r.Status != Rejected && r.Status != Unknown) || r.ReceivedAt.IsZero() || r.ReceivedAt.Location() != time.UTC || r.UpdatedAt.IsZero() || r.UpdatedAt.Location() != time.UTC || r.UpdatedAt.Before(r.ReceivedAt) || len(r.RawSHA256) != 64 || RawSHA256(r.Raw) != r.RawSHA256 || !json.Valid(r.Raw) {
		return fmt.Errorf("invalid agent receipt")
	}
	if r.SourceClientID != "" && !protocol.IsValidID(r.SourceClientID) {
		return fmt.Errorf("invalid agent receipt source client")
	}
	if r.TaskID != "" && !protocol.IsValidID(r.TaskID) {
		return fmt.Errorf("invalid agent receipt task")
	}
	if r.ObservedVersion < 0 || r.CurrentVersion < 0 {
		return fmt.Errorf("invalid agent receipt version")
	}
	for _, eventID := range r.AppliedEventIDs {
		if eventID < 1 {
			return fmt.Errorf("invalid agent receipt event")
		}
	}
	return nil
}

func (r Receipt) validateReceived() error {
	if r.Status != Received || r.Reason != "" || len(r.AppliedEventIDs) != 0 {
		return fmt.Errorf("invalid received agent receipt")
	}
	return r.validateAny()
}

func (r Receipt) validateFinal() error {
	if r.Status == Received {
		return fmt.Errorf("agent receipt is not final")
	}
	if r.Status == Accepted && r.Reason != "" {
		return fmt.Errorf("accepted agent receipt has a reason")
	}
	if (r.Status == Rejected || r.Status == Unknown) && r.Reason == "" {
		return fmt.Errorf("rejected agent receipt requires a reason")
	}
	return r.validateAny()
}

func sameReceiptOutcome(first, second Receipt) bool {
	return first.Status == second.Status && first.Reason == second.Reason && first.SourceClientID == second.SourceClientID && first.TaskID == second.TaskID && first.PackageID == second.PackageID && first.ObservedVersion == second.ObservedVersion && first.CurrentVersion == second.CurrentVersion && first.RawSHA256 == second.RawSHA256 && slicesEqual(first.AppliedEventIDs, second.AppliedEventIDs)
}

func slicesEqual(first, second []int64) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func stringsTrimSuffix(value, suffix string) string {
	return value[:len(value)-len(suffix)]
}
