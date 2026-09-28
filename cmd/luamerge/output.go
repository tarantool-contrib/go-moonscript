package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// outputDirPerm is the permission mode used when creating the output
	// directory for --output.
	outputDirPerm = 0o750
	// outputFilePerm is the mode a new output file is created with before the
	// umask applies, as os.Create does it.
	outputFilePerm fs.FileMode = 0o666
	// tempAttempts bounds the search for an unused temporary file name.
	tempAttempts = 100
	// tempRandomLen is the number of random characters in a temporary file
	// name; O_EXCL, not the length, is what rules out a clash.
	tempRandomLen = 8
)

var (
	// errOutputIsInput is returned when the output is one of the bundle's
	// inputs.
	errOutputIsInput = errors.New("refusing to overwrite an input")
	// errNoTempName is returned when every temporary file name tried was
	// taken.
	errNoTempName = errors.New("no unused temporary file name")
)

// putOutput delivers the finished bundle: to stdout for "-", otherwise to the
// file at path, which must not be one of inputs. It is called only once the
// build has succeeded, so a failed build prints nothing and changes nothing on
// disk: not an input, not a previous bundle, not a directory.
func putOutput(path string, data []byte, inputs []string) error {
	if path == "-" {
		_, err := os.Stdout.Write(data)
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}

		return nil
	}

	err := checkNotInput(path, inputs)
	if err != nil {
		return err
	}

	return writeOutput(path, data)
}

// checkNotInput returns an error when path names an existing regular file
// that is one of inputs, so writing the bundle there would destroy a file the
// bundle is built from. Files are compared by identity (os.SameFile), which
// also catches a differently spelled path, a symlink or a hard link to an
// input. An input that does not exist, such as a config file that was never
// there, cannot be overwritten and is skipped.
func checkNotInput(path string, inputs []string) error {
	output, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("check output: %w", err)
	}

	if !output.Mode().IsRegular() {
		// Writing to a device such as /dev/null, or to a FIFO, destroys
		// nothing, even when the same device was read as an input.
		return nil
	}

	for _, input := range inputs {
		info, statErr := os.Stat(input)
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}

		if statErr != nil {
			return fmt.Errorf("check output: %w", statErr)
		}

		if os.SameFile(output, info) {
			return fmt.Errorf("%w: output %s is the same file as input %s", errOutputIsInput, path, input)
		}
	}

	return nil
}

// writeOutput puts the finished bundle at path, creating missing parent
// directories.
//
// A regular file, or a path that does not exist yet, is replaced atomically
// (see replaceFile): a failed or interrupted write never leaves a truncated
// bundle behind, and a reader sees either the old bundle or the new one. An
// existing file must be writable itself, not only its directory.
//
// Anything else is written in place, as os.Create did: a symlink is followed
// and its target rewritten (a dangling link's target is created), a device
// such as /dev/null or a FIFO is written to rather than replaced, and a
// directory is an error.
func writeOutput(path string, data []byte) error {
	info, err := os.Lstat(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		err = os.MkdirAll(filepath.Dir(path), outputDirPerm)
		if err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}

		return replaceFile(path, data, nil)
	case err != nil:
		return fmt.Errorf("write output: %w", err)
	case info.Mode().IsRegular():
		err = checkWritable(path)
		if err != nil {
			return err
		}

		return replaceFile(path, data, info)
	default:
		err = os.WriteFile(path, data, outputFilePerm)
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}

		return nil
	}
}

// checkWritable returns an error unless the existing file at path may be
// written to. The rename in replaceFile needs only the directory to be
// writable; refusing a file that is not writable itself, as os.Create did,
// keeps a bundle someone made read-only from being replaced behind their
// back. The file is opened without truncation, so it stays untouched.
func checkWritable(path string) error {
	// #nosec G304 -- path is the --output the user chose; it is opened to be written, not read.
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// replaceFile writes data to a temporary file in the directory of path and
// renames it over path. old describes the regular file being replaced, or is
// nil when path does not exist yet. The new file keeps the permission bits of
// old, so an output made executable with chmod +x stays executable; a new one
// gets 0666 less the umask, as os.Create would give it. On failure the
// temporary file is removed and path is left as it was.
func replaceFile(path string, data []byte, old fs.FileInfo) error {
	perm := outputFilePerm
	if old != nil {
		perm = old.Mode().Perm()
	}

	tmp, err := createTemp(filepath.Dir(path), filepath.Base(path), perm)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	_, err = tmp.Write(data)
	if err == nil && old != nil {
		// Creating the file applied the umask; set the old bits exactly.
		err = tmp.Chmod(perm)
	}

	if err == nil {
		err = tmp.Sync()
	}

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		// Report a failed cleanup along with the cause, not instead of it.
		return fmt.Errorf("write output: %w", errors.Join(err, os.Remove(tmp.Name())))
	}

	return nil
}

// createTemp creates a new, empty file in dir to be renamed to base later.
// Unlike os.CreateTemp, which always uses 0600, it takes the permission bits,
// so the umask shapes the file exactly as it shapes one made by os.Create.
func createTemp(dir, base string, perm fs.FileMode) (*os.File, error) {
	for range tempAttempts {
		name := filepath.Join(dir, "."+base+"."+rand.Text()[:tempRandomLen]+".tmp")

		// #nosec G304 -- a new file next to the --output the user chose; O_EXCL never opens an existing one.
		file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("create temporary file: %w", err)
		}

		return file, nil
	}

	return nil, fmt.Errorf("%w in %s", errNoTempName, dir)
}
