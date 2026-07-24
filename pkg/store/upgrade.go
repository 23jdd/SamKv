package store

// CurrentSSTableVersion 返回当前写入的 SSTable 格式版本。
const CurrentSSTableVersion = currentSSTableVersion

// UpgradeResult 描述一次存储格式升级的结果。
type UpgradeResult struct {
	RewrittenTables int
	OutputPath      string
	SSTableVersion  uint32
	ManifestVersion uint32
}

// UpgradeFormat 先执行 Checkpoint，再把旧版 SSTable 重写为当前格式。
func (st *StoreManger) UpgradeFormat() (UpgradeResult, error) {
	if _, err := st.Checkpoint(); err != nil {
		return UpgradeResult{}, err
	}

	st.mu.RLock()
	if st.closed {
		st.mu.RUnlock()
		return UpgradeResult{}, ErrStoreClosed
	}
	legacyTables := 0
	for _, table := range st.sstables {
		if table.Version() < currentSSTableVersion {
			legacyTables++
		}
	}
	manifestVersion := st.manifest.FormatVersion
	st.mu.RUnlock()

	result := UpgradeResult{
		RewrittenTables: legacyTables,
		SSTableVersion:  currentSSTableVersion,
		ManifestVersion: CurrentManifestVersion,
	}
	if legacyTables == 0 && manifestVersion == CurrentManifestVersion {
		return result, nil
	}
	compaction, err := st.compactAll(true)
	result.OutputPath = compaction.Path
	if err != nil {
		return result, err
	}

	st.mu.Lock()
	nextManifest := st.manifest
	nextManifest.FormatVersion = CurrentManifestVersion
	if err := saveManifest(st.dir, nextManifest); err != nil {
		st.mu.Unlock()
		return result, err
	}
	st.manifest = nextManifest
	st.mu.Unlock()
	return result, nil
}
