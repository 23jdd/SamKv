package store

// 本文件创建带 SHA-256 清单的一致性备份，校验备份，并把备份原子恢复到新目录。
// 备份是本地文件副本，不包含跨主机传输、加密、增量快照或远端保留策略。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/23jdd/SamKv/pkg/wal"
)

const (
	backupMetadataFile          = "BACKUP.json"
	CurrentBackupVersion uint32 = 1
)

// BackupFile 记录备份文件的大小和 SHA-256 校验值。
type BackupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BackupMetadata 描述一个可验证的 Store 备份集。
type BackupMetadata struct {
	FormatVersion   uint32       `json:"format_version"`
	CreatedAt       time.Time    `json:"created_at"`
	ManifestVersion uint32       `json:"manifest_version"`
	SSTableVersion  uint32       `json:"sstable_version"`
	Files           []BackupFile `json:"files"`
}

// Backup 先执行 Checkpoint，再把一致的数据文件复制到新的备份目录。
// destination 必须尚不存在且不能位于数据目录内部；复制期间维护锁和读锁会阻止文件集合变化。
func (st *StoreManger) Backup(destination string) (BackupMetadata, error) {
	if _, err := st.Checkpoint(); err != nil {
		return BackupMetadata{}, err
	}
	dataDir, err := filepath.Abs(st.dir)
	if err != nil {
		return BackupMetadata{}, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return BackupMetadata{}, err
	}
	if pathWithin(dataDir, destination) {
		return BackupMetadata{}, errors.New("store: backup destination must be outside data directory")
	}
	if _, err := os.Stat(destination); err == nil {
		return BackupMetadata{}, errors.New("store: backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return BackupMetadata{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return BackupMetadata{}, err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-")
	if err != nil {
		return BackupMetadata{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()

	st.maintenanceMu.Lock()
	defer st.maintenanceMu.Unlock()
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.closed {
		return BackupMetadata{}, ErrStoreClosed
	}
	if err := st.wm.Flush(); err != nil {
		return BackupMetadata{}, err
	}
	if _, err := os.Stat(manifestPath(st.dir)); errors.Is(err, os.ErrNotExist) {
		if err := saveManifest(st.dir, st.manifest); err != nil {
			return BackupMetadata{}, err
		}
	}

	fileNames := []string{manifestFileName}
	segments, err := wal.ListSegments(st.dir)
	if err != nil {
		return BackupMetadata{}, err
	}
	for _, segment := range segments {
		fileNames = append(fileNames, filepath.Base(segment.Path))
	}
	for _, entry := range st.manifest.SSTables {
		fileNames = append(fileNames, entry.File)
	}
	metadata := BackupMetadata{
		FormatVersion:   CurrentBackupVersion,
		CreatedAt:       time.Now().UTC(),
		ManifestVersion: st.manifest.FormatVersion,
		SSTableVersion:  currentSSTableVersion,
		Files:           make([]BackupFile, 0, len(fileNames)),
	}
	for _, name := range fileNames {
		source := filepath.Join(st.dir, name)
		target := filepath.Join(temporary, name)
		if err := copyFile(source, target); err != nil {
			return BackupMetadata{}, err
		}
		file, err := inspectBackupFile(target)
		if err != nil {
			return BackupMetadata{}, err
		}
		file.Name = name
		metadata.Files = append(metadata.Files, file)
	}
	if err := writeBackupMetadata(filepath.Join(temporary, backupMetadataFile), metadata); err != nil {
		return BackupMetadata{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return BackupMetadata{}, err
	}
	published = true
	return metadata, nil
}

// VerifyBackup 校验备份文件摘要、Manifest 引用和 SSTable 完整性。
// 清单之外的额外文件会被忽略；清单内缺失、摘要不符或 SSTable 损坏都会返回错误。
func VerifyBackup(source string) (BackupMetadata, error) {
	data, err := os.ReadFile(filepath.Join(source, backupMetadataFile))
	if err != nil {
		return BackupMetadata{}, err
	}
	var metadata BackupMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return BackupMetadata{}, fmt.Errorf("store: decode backup metadata: %w", err)
	}
	if metadata.FormatVersion == 0 || metadata.FormatVersion > CurrentBackupVersion {
		return BackupMetadata{}, fmt.Errorf("store: unsupported backup version %d", metadata.FormatVersion)
	}
	available := make(map[string]struct{}, len(metadata.Files))
	for _, expected := range metadata.Files {
		if expected.Name == "" || filepath.Base(expected.Name) != expected.Name {
			return BackupMetadata{}, errors.New("store: invalid backup file name")
		}
		actual, err := inspectBackupFile(filepath.Join(source, expected.Name))
		if err != nil {
			return BackupMetadata{}, err
		}
		if actual.Size != expected.Size || actual.SHA256 != expected.SHA256 {
			return BackupMetadata{}, fmt.Errorf("store: backup checksum mismatch for %s", expected.Name)
		}
		available[expected.Name] = struct{}{}
	}
	if _, ok := available[manifestFileName]; !ok {
		return BackupMetadata{}, fmt.Errorf("store: backup is missing %s", manifestFileName)
	}
	hasWALSegment := false
	for name := range available {
		if _, ok := wal.ParseSegmentID(name); ok {
			hasWALSegment = true
			break
		}
	}
	if !hasWALSegment {
		return BackupMetadata{}, errors.New("store: backup is missing WAL segments")
	}
	manifest, err := readManifest(filepath.Join(source, manifestFileName))
	if err != nil {
		return BackupMetadata{}, err
	}
	for _, entry := range manifest.SSTables {
		if _, ok := available[entry.File]; !ok {
			return BackupMetadata{}, fmt.Errorf("store: backup is missing %s", entry.File)
		}
		table, err := OpenSStable(filepath.Join(source, entry.File))
		if err != nil {
			return BackupMetadata{}, err
		}
		_, verifyErr := table.Verify()
		closeErr := table.Close()
		if err := errors.Join(verifyErr, closeErr); err != nil {
			return BackupMetadata{}, err
		}
	}
	return metadata, nil
}

// RestoreBackup 将校验通过的备份恢复到一个尚不存在的数据目录。
// destination 的父目录可以不存在，但 destination 本身存在时绝不覆盖。
func RestoreBackup(source, destination string) error {
	metadata, err := VerifyBackup(source)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return errors.New("store: restore destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".restore-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()
	for _, file := range metadata.Files {
		if err := copyFile(filepath.Join(source, file.Name), filepath.Join(temporary, file.Name)); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(source, backupMetadataFile), filepath.Join(temporary, backupMetadataFile)); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	published = true
	return nil
}

func inspectBackupFile(path string) (BackupFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return BackupFile{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return BackupFile{}, err
	}
	return BackupFile{Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func writeBackupMetadata(path string, metadata BackupMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if err := writeAll(file, data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
