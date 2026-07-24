package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/23jdd/SamKv/pkg/store"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("command is required")
	}
	switch args[0] {
	case "verify":
		flags := newFlagSet("verify", stderr)
		dir := flags.String("dir", "", "Store data directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" {
			return errors.New("-dir is required")
		}
		database, err := openAdministrativeStore(*dir)
		if err != nil {
			return err
		}
		report, verifyErr := database.Verify()
		return errors.Join(writeJSON(stdout, report), verifyErr, database.Close())
	case "repair":
		flags := newFlagSet("repair", stderr)
		dir := flags.String("dir", "", "Store data directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" {
			return errors.New("-dir is required")
		}
		report, err := store.RepairDirectory(*dir)
		return errors.Join(writeJSON(stdout, report), err)
	case "backup":
		flags := newFlagSet("backup", stderr)
		dir := flags.String("dir", "", "Store data directory")
		destination := flags.String("dest", "", "New backup directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" || *destination == "" {
			return errors.New("-dir and -dest are required")
		}
		database, err := openAdministrativeStore(*dir)
		if err != nil {
			return err
		}
		metadata, backupErr := database.Backup(*destination)
		return errors.Join(writeJSON(stdout, metadata), backupErr, database.Close())
	case "verify-backup":
		flags := newFlagSet("verify-backup", stderr)
		source := flags.String("source", "", "Backup directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *source == "" {
			return errors.New("-source is required")
		}
		metadata, err := store.VerifyBackup(*source)
		return errors.Join(writeJSON(stdout, metadata), err)
	case "restore":
		flags := newFlagSet("restore", stderr)
		source := flags.String("source", "", "Backup directory")
		destination := flags.String("dest", "", "New Store data directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *source == "" || *destination == "" {
			return errors.New("-source and -dest are required")
		}
		if err := store.RestoreBackup(*source, *destination); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]string{"status": "ok"})
	case "upgrade":
		flags := newFlagSet("upgrade", stderr)
		dir := flags.String("dir", "", "Store data directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" {
			return errors.New("-dir is required")
		}
		database, err := openAdministrativeStore(*dir)
		if err != nil {
			return err
		}
		result, upgradeErr := database.UpgradeFormat()
		return errors.Join(writeJSON(stdout, result), upgradeErr, database.Close())
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func openAdministrativeStore(dir string) (*store.StoreManager, error) {
	options := store.DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	return store.NewStoreManagerWithOptions(dir, options)
}

func newFlagSet(name string, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	return flags
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  samkv-admin verify -dir <data-dir>")
	fmt.Fprintln(output, "  samkv-admin repair -dir <data-dir>")
	fmt.Fprintln(output, "  samkv-admin backup -dir <data-dir> -dest <backup-dir>")
	fmt.Fprintln(output, "  samkv-admin verify-backup -source <backup-dir>")
	fmt.Fprintln(output, "  samkv-admin restore -source <backup-dir> -dest <data-dir>")
	fmt.Fprintln(output, "  samkv-admin upgrade -dir <data-dir>")
}
