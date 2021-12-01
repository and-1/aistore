// Package cmn provides common constants, types, and utilities for AIS clients
// and AIStore.
/*
 * Copyright (c) 2018-2021, NVIDIA CORPORATION. All rights reserved.
 */
package cos

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// Checks if the error is generated by any IO operation and if the error
// is severe enough to run the FSHC for mountpath testing
//
// For mountpath definition, see fs/mountfs.go
func IsIOError(err error) bool {
	if err == nil {
		return false
	}

	ioErrs := []error{
		io.ErrShortWrite,

		syscall.EIO,     // I/O error
		syscall.ENOTDIR, // mountpath is missing
		syscall.EBUSY,   // device or resource is busy
		syscall.ENXIO,   // No such device
		syscall.EBADF,   // Bad file number
		syscall.ENODEV,  // No such device
		syscall.EROFS,   // readonly filesystem
		syscall.EDQUOT,  // quota exceeded
		syscall.ESTALE,  // stale file handle
		syscall.ENOSPC,  // no space left
	}
	for _, ioErr := range ioErrs {
		if errors.Is(err, ioErr) {
			return true
		}
	}
	return false
}

func IsErrXattrNotFound(err error) bool {
	// NOTE: syscall.ENOATTR confirmed to be returned on Darwin, instead of syscall.ENODATA.
	return os.IsNotExist(err) || err == syscall.ENOATTR
}
