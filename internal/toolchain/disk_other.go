//go:build !unix

package toolchain

func freeBytes(string) int64 { return -1 }
