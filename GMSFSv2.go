package GMSFS

import (
	"fmt"
	cmap "github.com/orcaman/concurrent-map/v2"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FileInfo stores comprehensive metadata about a file or directory
type FileInfo struct {
	Exists       bool
	Size         int64
	Mode         os.FileMode
	LastModified time.Time
	IsDir        bool
	Contents     []FileInfo // Names of files for directories
	Name         string
}

const timeFlat = "20060102_1504"

var CachedFiles = cmap.New[[]byte]()
var CachedFileNames = cmap.New[bool]()

// FileHandleInstance to store file and timer information
type FileHandleInstance struct {
	File  *os.File
	Timer *time.Timer
}

// errorPrinter logs errors to a debug file if it exists, or to a log file with a timestamp and stack trace if not.
// log - The error message to be logged.
// object - The object associated with the error.
func errorPrinter(log string, object string) {
	if _, err := os.Stat("GMSFS.Debug"); err != nil {
		if os.IsNotExist(err) {
			return
		}
	}

	stack := ""
	pc, _, _, ok := runtime.Caller(2) // 2 level up the call stack
	if ok {
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			file, line := fn.FileLine(fn.Entry())
			stack = " (2):" + fn.Name() + " file: " + file + " line: " + strconv.Itoa(line)
		}
	}

	AppendStringToFile("GMSFS."+time.Now().Format(timeFlat)+".log", log+" stacktrace: "+stack+"\r\n")
}

// cleanPath sanitizes a given path by cleaning up redundant separators and resolving symbolic links.
// It removes any device information if present, returning the cleaned path.
func cleanPath(path string) string {
	path = filepath.Clean(path)
	fs := strings.SplitN(path, ":", 2)
	if len(fs) == 2 {
		path = fs[1]
	}

	return path
}

// OpenFile opens a file with the given name, flag, and permissions.
// If an error occurs, it logs the error and returns nil along with the error.
func OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	file, err := os.OpenFile(name, flag, perm)
	if err != nil {
		errorPrinter("OpenFile: "+err.Error(), name)
		return nil, err
	}
	return file, nil
}

// Open opens the file with the specified name after cleaning the path.
// name - The name of the file to be opened.
// Returns a pointer to the opened file and an error if the file cannot be opened.
func Open(name string) (*os.File, error) {
	name = cleanPath(name)

	// Open the file using os.Open
	file, err := os.Open(name)
	if err != nil {
		errorPrinter("Open: "+err.Error(), name)
		return nil, err
	}

	return file, nil
}

// Create creates a new file named by the provided name.
// Returns a pointer to the created file and an error if something went wrong.
func Create(name string) (*os.File, error) {
	name = cleanPath(name)

	file, err := os.Create(name)
	if err != nil {
		errorPrinter("Create: "+err.Error(), name)
		return nil, err
	}

	cacheClearElement(name)

	return file, nil
}

// CopyDir recursively copies a directory from src to dst. It replicates the directory structure and file contents.
// src must be a directory; if dst already exists, an error is returned. Symlinks within the directory are skipped.
func CopyDir(src string, dst string) error {
	src = cleanPath(src)
	dst = cleanPath(dst)

	si, err := os.Stat(src) // Directly use os.Stat
	if err != nil {
		errorPrinter("CopyDir (os.Stat): "+err.Error(), src)
		return err
	}
	if !si.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		errorPrinter("CopyDir: File already exist", dst)
		return fmt.Errorf("destination already exists")
	}

	err = os.MkdirAll(dst, si.Mode())
	if err != nil {
		errorPrinter("CopyDir (os.MkdirAll): "+err.Error(), dst)
		return err
	}

	entries, err := os.ReadDir(src) // Directly use os.ReadDir
	if err != nil {
		errorPrinter("CopyDir (os.ReadDir): "+err.Error(), src)
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = CopyDir(srcPath, dstPath)
			if err != nil {
				errorPrinter("CopyDir (CopyDir-1): "+err.Error(), srcPath)
				errorPrinter("CopyDir (CopyDir-2): "+err.Error(), dstPath)
				return err
			}
		} else {
			// Skip symlinks
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}

			err = CopyFile(srcPath, dstPath)
			if err != nil {
				errorPrinter("CopyDir (CopyFile-1): "+err.Error(), srcPath)
				errorPrinter("CopyDir (CopyFile-2): "+err.Error(), dstPath)
				return err
			}
		}
	}

	clearCacheForPath(dst)

	return nil
}

// Delete removes the specified file from the filesystem.
// name - The path of the file to be deleted.
// Returns an error if the file cannot be removed.
func Delete(name string) error {
	// Remove the file from the filesystem
	err := os.Remove(name) // Use original case for filesystem operations
	if err != nil {
		errorPrinter("Delete: "+err.Error(), name)
		return err
	}

	cacheClearElement(name)

	return nil
}

// ReadFile reads the content of the file specified by the given name and returns it as a byte slice.
// It returns an error if the file does not exist or an error occurred during reading.
func ReadFile(name string) ([]byte, error) {
	// Read the file contents
	content, err := os.ReadFile(name) // Use the original case for filesystem operations
	if err != nil {
		errorPrinter("ReadFile: "+err.Error(), name)
		return nil, err
	}

	return content, nil
}

// FileExists checks whether a file or directory with the specified name exists and returns true if it does, otherwise false.
func FileExists(name string) bool {
	c, ok := CachedFileNames.Get(strings.ToLower(name))
	if ok == true {
		return c
	}

	_, err := os.Stat(name)
	if os.IsNotExist(err) {
		return false
	} else if err == nil {
		return true
	}
	return false
}

// Mkdir creates a new directory with the specified name and permissions.
// name - The name of the directory to create.
// perm - The permissions to use when creating the directory.
// Returns an error if there is an issue creating the directory.
func Mkdir(name string, perm os.FileMode) error {
	name = cleanPath(name) // Preserve original name for file operation
	err := os.Mkdir(name, perm)
	if err != nil {
		errorPrinter("Mkdir: "+err.Error(), name)
		return err
	}

	clearCacheForPath(name)

	return nil
}

// MkdirAll creates a directory named path along with any necessary parents using the specified file permissions.
func MkdirAll(path string, perm os.FileMode) error {
	path = cleanPath(path) // Preserve original path for file operation

	if FileExists(path) == true {
		return nil
	}

	err := os.MkdirAll(path, perm)
	if err != nil {
		return err
	}

	clearCacheForPath(path)

	return nil
}

// Append appends the given byte content to the specified file.
// If the file does not exist, it will be created with the specified content.
// name - The path of the file to which the content will be appended.
// content - The byte content to append to the file.
// Returns an error if there is an issue opening, creating, or writing to the file.
func Append(name string, content []byte) error {
	var file *os.File
	var err error

	// If not, open the file and store the handle in the map
	file, err = os.OpenFile(name, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the content to the file
	_, err = file.Write(content)
	if err != nil {
		errorPrinter("Append: "+err.Error(), name)
		return err
	}

	cacheClearElement(name)

	return nil
}

// AppendStringToFile appends the given string content to the specified file.
// If the file does not exist, it will be created with the specified content.
// name - The path of the file to which the content will be appended.
// content - The string content to append to the file.
// Returns an error if there is an issue opening, creating, or writing to the file.
func AppendStringToFile(name string, content string) error {
	return Append(name, []byte(content))
}

// WriteFile writes data to the named file with specified permissions. It cleans the file path before writing.
func WriteFile(name string, content []byte, perm os.FileMode) error {
	name = cleanPath(name)

	cacheClearElement(name)

	// Write the new content to the file
	err := os.WriteFile(name, content, perm)

	if err != nil {
		return err
	}

	return nil
}

// FileSize returns the size of the file specified by the name parameter.
// If the file does not exist or an error occurs, it returns 0 and the error.
func FileSize(name string) (int64, error) {
	// If not in cache, get file size from the filesystem
	stat, err := os.Stat(name) // Original name for filesystem operation
	if err != nil {
		errorPrinter("FileSize: "+err.Error(), name)
		return 0, err // File does not exist or other error occurred
	}

	return stat.Size(), nil
}

// FileSizeZeroOnError returns the size of the file specified by `name`. If an error occurs, it returns 0.
func FileSizeZeroOnError(name string) int64 {
	// If not in cache, get file size from the filesystem
	stat, err := os.Stat(name) // Original name for filesystem operation
	if err != nil {
		return 0 // Return 0 if file does not exist or other error occurred
	}

	return stat.Size()
}

// Rename renames a file from oldName to newName. Returns an error if the renaming fails.
func Rename(oldName, newName string) error {
	if oldName == newName {
		return nil
	}

	cacheClearElement(oldName)
	cacheClearElement(newName)

	err := os.Rename(oldName, newName)
	if err != nil {
		errorPrinter("Rename: "+err.Error(), oldName)
		errorPrinter("Rename: "+err.Error(), newName)
		return err
	}

	return nil
}

// CopyFile copies the contents of a file from src to dst. If dst does not exist, it is created with the same permissions as src.
func CopyFile(src, dst string) (err error) {
	cacheClearElement(dst)

	src = cleanPath(src)
	dst = cleanPath(dst)

	in, err := os.Open(src)
	if err != nil {
		errorPrinter("CopyFile (os.Open): "+err.Error(), src)
		return
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		errorPrinter("CopyFile (os.Create): "+err.Error(), dst)
		return
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		errorPrinter("CopyFile (io.Copy): "+err.Error(), "")
		return
	}

	err = out.Sync()
	if err != nil {
		errorPrinter("CopyFile (out.Sync): "+err.Error(), "")
		return
	}

	si, err := os.Stat(src)
	if err != nil {
		errorPrinter("CopyFile (os.Stat): "+err.Error(), "")
		return
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		errorPrinter("CopyFile (os.Chmod): "+err.Error(), "")
		return
	}

	return
}

// Remove deletes the named file. If an error occurs, it logs the error and returns it.
func Remove(name string) error {
	cacheClearElement(name)

	err := os.Remove(name)
	if err != nil {
		errorPrinter("Remove: "+err.Error(), name)
		return err
	}

	return nil
}

// RemoveAll deletes the specified path and any children it contains, effectively emptying the directory if it exists.
func RemoveAll(path string) error {
	path = cleanPath(path)
	oserr := os.RemoveAll(path)

	clearCacheForPath(path)

	return oserr
}

// ListFS traverses a directory at the specified path and returns a list of names of files and subdirectories.
// Directories are prefixed with "*". Errors are logged, and an empty slice is returned on failure.
func ListFS(path string) []string {
	var sysSlices []string

	// First, check if the path is a directory
	fileInfo, err := Stat(path)
	if err != nil {
		errorPrinter("ListFS (Stat): "+err.Error(), path)
		return sysSlices // Return empty slice if there's an error
	}
	if !fileInfo.IsDir {
		return sysSlices // Return empty slice if it's not a directory
	}

	//Build the directory from disk
	objs, err := ReadDir(path)
	if err == nil {
		for _, fi := range objs {
			if fi.IsDir {
				sysSlices = append(sysSlices, "*"+fi.Name)
			} else {
				sysSlices = append(sysSlices, fi.Name)
			}
		}
	} else {
		errorPrinter("ListFS: 9"+err.Error(), "")
	}
	return sysSlices
}

// RecurseFS traverses the filesystem starting from the specified path, collecting paths of all files and directories.
// Directories are prefixed with an asterisk (*). The function returns a slice containing all of the collected paths.
func RecurseFS(path string) (sysSlices []string) {
	//	temp, ok := FileCache.Get(lowerCasePath)
	var files []FileInfo

	stat, err := Stat(path)
	if err != nil {
		return sysSlices
	}

	temp, err := ReadDir(path)
	if err != nil {
		return sysSlices
	}

	if stat.IsDir {
		for _, name := range temp {
			fileInfo, err := Stat(filepath.Join(path, name.Name)) // Use original path for stat
			if err != nil {
				errorPrinter("RecureseFS (Stat): "+err.Error(), filepath.Join(path, name.Name))
				continue // Handle error as needed
			}
			files = append(files, fileInfo)
		}
	}

	for _, f := range files {
		fullPath := path + "/" + f.Name
		if f.IsDir {
			sysSlices = append(sysSlices, "*"+fullPath)
			childSlices := RecurseFS(fullPath)
			sysSlices = append(sysSlices, childSlices...)
		} else {
			sysSlices = append(sysSlices, fullPath)
		}
	}

	return sysSlices
}

// FileAgeInSec returns the age of the file specified by filename in seconds or an error if the file doesn't exist or is inaccessible.
func FileAgeInSec(filename string) (age time.Duration, err error) {
	// If not in cache, get file info from the filesystem and update the cache
	var stat FileInfo
	stat, err = Stat(filename)
	if err != nil {
		errorPrinter("FileAgeInSec: "+err.Error(), filename)
		return -1, err
	}

	return time.Now().Sub(stat.LastModified), nil
}

// CopyDirFilesGlob copies files from the source directory to the destination directory matching the file pattern.
func CopyDirFilesGlob(src string, dst string, fileMatch string) (err error) {
	src = cleanPath(src)
	dst = cleanPath(dst)

	// Check if source is a directory
	srcInfo, err := Stat(src) // Use cached Stat
	if err != nil {
		errorPrinter("CopyDirFilesGlob: "+err.Error(), src)
		return fmt.Errorf("source is not a directory or does not exist")
	}
	if !srcInfo.IsDir {
		return fmt.Errorf("source is not a directory or does not exist")
	}

	// Create destination directory if it doesn't exist
	if !FileExists(dst) {
		err = MkdirAll(dst, srcInfo.Mode) // Use cached MkdirAll
		if err != nil {
			errorPrinter("CopyDirFilesGlob (MkdirAll): "+err.Error(), dst)
			return
		}
	}

	// Use CachedGlob to match files
	matches, err := Glob(src + "/" + fileMatch)
	if err != nil {
		errorPrinter("CopyDirFilesGlob (Glob): "+err.Error(), src+"/"+fileMatch)
		return err
	}

	for _, item := range matches {
		itemBaseName := filepath.Base(item)
		err = CopyFile(item, filepath.Join(dst, itemBaseName)) // Use cached CopyFile
		if err != nil {
			errorPrinter("CopyDirFilesGlob (CopyFile-1): "+err.Error(), item)
			errorPrinter("CopyDirFilesGlob (CopyFile-2): "+err.Error(), filepath.Join(dst, itemBaseName))
			return
		}
	}

	clearCacheForPath(dst)

	return nil
}

// FindFilesInDir searches for files in the specified directory that match the given pattern and returns their paths.
func FindFilesInDir(dir string, pattern string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, entry := range entries {
		if matched, err := filepath.Match(pattern, entry.Name()); err != nil {
			return nil, err
		} else if matched {
			matches = append(matches, filepath.Join(dir, entry.Name()))
		}
	}

	return matches, nil
}

// Glob returns the names of all files matching the specified pattern, or an error if the pattern is invalid.
func Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	return matches, nil
}

// Stat retrieves the FileInfo for the specified file or directory name and returns it along with any error encountered.
func Stat(name string) (FileInfo, error) {
	stat, err := os.Stat(name)
	if err != nil {
		return FileInfo{}, err
	}

	dirNameOnly := filepath.Base(name)
	info := FileInfo{
		Exists:       true,
		Size:         stat.Size(),
		Mode:         stat.Mode(),
		LastModified: stat.ModTime(),
		IsDir:        stat.IsDir(),
		Name:         dirNameOnly,
	}

	return info, nil
}

// ReadDir reads the contents of a directory specified by dirName and returns a slice of FileInfo objects and an error.
func ReadDir(dirName string) ([]FileInfo, error) {
	// Open the directory
	f, err := os.Open(dirName)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Read the directory entries
	dirs, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	// Sort the directory entries by name
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })

	// Convert the directory entries to FileInfo objects
	var fileInfos []FileInfo
	for _, entry := range dirs {
		entryStat, err := entry.Info()
		if err != nil {
			return nil, err
		}

		fileInfo := FileInfo{
			Exists:       true,
			Size:         entryStat.Size(),
			Mode:         entryStat.Mode(),
			LastModified: entryStat.ModTime(),
			IsDir:        entryStat.IsDir(),
			Name:         entryStat.Name(),
		}

		fileInfos = append(fileInfos, fileInfo)
	}

	return fileInfos, nil
}

func cacheClearElement(file string) {
	CachedFiles.Remove(file)
	CachedFileNames.Remove(file)
}

func clearCacheForPath(prefix string) {
	cfne := CachedFileNames.Items()
	prefixLower := strings.ToLower(prefix)
	for name := range cfne {
		if strings.HasPrefix(strings.ToLower(name), prefixLower) {
			cacheClearElement(name)
		}
	}
}

// CacheReadFile reads the file and caches its content if not already cached for faster subsequent reads.
func CacheReadFile(file string) (data []byte, err error) {
	cfe, isok := CachedFileNames.Get(file)
	if isok == true {
		if cfe == true {
			d, ok := CachedFiles.Get(file)
			if ok == true {
				return d, nil
			}
		} else {
			return nil, fmt.Errorf("File not found: %s", file)
		}
	}

	d, err := ReadFile(file)
	if err == nil {
		CachedFiles.Set(file, d)
		CachedFileNames.Set(file, true)
	} else {
		CachedFileNames.Set(file, false)
	}
	return d, err
}

func CacheWriteFile(file string, data []byte, perm os.FileMode) (err error) {
	err = WriteFile(file, data, perm)
	if err == nil {
		CachedFiles.Set(file, data)
	}
	return err
}
