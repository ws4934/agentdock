package file

import (
	"io"
	"os"
)

type boundedFileRead struct {
	Info      os.FileInfo
	Data      []byte
	Size      int64
	TooLarge  bool
	BytesRead int64
}

func readBoundedFile(path string, maxBytes int64) (boundedFileRead, error) {
	// 在 open 前排除命名管道和设备，避免文本读取因没有写入端而无限阻塞。
	before, err := os.Stat(path)
	if err != nil {
		return boundedFileRead{}, err
	}
	if !before.IsDir() {
		if err := ensureRegular(before); err != nil {
			return boundedFileRead{}, err
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return boundedFileRead{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return boundedFileRead{}, err
	}
	result := boundedFileRead{Info: info, Size: info.Size()}
	if !os.SameFile(before, info) {
		return result, toolError("FILE_CHANGED", "file replaced while opening", "conflict")
	}
	if info.IsDir() {
		return result, nil
	}
	if err := ensureRegular(info); err != nil {
		return result, err
	}
	if info.Size() > maxBytes {
		result.TooLarge = true
		return result, nil
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	result.BytesRead = int64(len(data))
	if err != nil {
		return result, err
	}
	after, err := file.Stat()
	if err != nil {
		return result, err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Mode() != info.Mode() {
		return result, toolError("FILE_CHANGED", "file changed while reading", "conflict")
	}
	result.Data = data
	if int64(len(data)) > result.Size {
		result.Size = int64(len(data))
	}
	result.TooLarge = int64(len(data)) > maxBytes
	if result.TooLarge {
		result.Data = nil
	}
	return result, nil
}
