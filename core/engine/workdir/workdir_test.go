/*
 * Copyright 2022 The Gremlins Authors
 *
 *    Licensed under the Apache License, Version 2.0 (the "License");
 *    you may not use this file except in compliance with the License.
 *    You may obtain a copy of the License at
 *
 *        http://www.apache.org/licenses/LICENSE-2.0
 *
 *    Unless required by applicable law or agreed to in writing, software
 *    distributed under the License is distributed on an "AS IS" BASIS,
 *    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *    See the License for the specific language governing permissions and
 *    limitations under the License.
 */

package workdir_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hectane/go-acl"

	"github.com/go-maxhub/gremlins/core/engine/workdir"
)

func TestCopyFolder(t *testing.T) {
	srcDir := t.TempDir()
	populateSrcDir(t, srcDir, 3)
	wdDir := t.TempDir()

	dealer := workdir.NewCachedDealer(wdDir, srcDir)
	defer dealer.Clean()

	dstDir, err := dealer.Get("test")
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.Walk(srcDir, checkForDifferentFile(t, srcDir, dstDir))
	if err != nil {
		t.Fatal(err)
	}
}

func checkForDifferentFile(t *testing.T, srcDir string, dstDir string) func(path string, srcFileInfo fs.FileInfo, err error) error {
	t.Helper()

	return func(path string, srcFileInfo fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			t.Fatal(err)
		}
		if relPath == "." {
			return nil
		}
		dstPath := filepath.Join(dstDir, relPath)
		dstFileInfo, err := os.Lstat(dstPath)
		if err != nil {
			t.Fatal(err)
		}

		sameFile := os.SameFile(dstFileInfo, srcFileInfo)
		if sameFile {
			t.Error("expected file to be different, got the same file")
		}

		if !cmp.Equal(dstFileInfo.Name(), srcFileInfo.Name()) {
			t.Errorf("expected Name to be %v, got %v", srcFileInfo.Name(), dstFileInfo.Name())
		}
		if !cmp.Equal(dstFileInfo.Mode(), srcFileInfo.Mode()) {
			t.Errorf(cmp.Diff(srcFileInfo.Mode(), dstFileInfo.Mode()))
		}

		return nil
	}
}

func TestCachesFolder(t *testing.T) {
	t.Run("caches copy folders", func(t *testing.T) {
		srcDir := t.TempDir()
		populateSrcDir(t, srcDir, 0)
		dstDir := t.TempDir()

		mngr := workdir.NewCachedDealer(dstDir, srcDir)
		defer mngr.Clean()

		firstDir, err := mngr.Get("worker-1")
		if err != nil {
			t.Fatal(err)
		}

		secondDir, err := mngr.Get("worker-1")
		if err != nil {
			t.Fatal(err)
		}

		thirdDir, err := mngr.Get("worker-2")
		if err != nil {
			t.Fatal(err)
		}

		if firstDir != secondDir {
			t.Errorf("expected dirs to be cached, got %s", cmp.Diff(firstDir, secondDir))
		}
		if firstDir == thirdDir {
			t.Errorf("expected a new dir to be instanciated")
		}
	})

	t.Run("cleans up all the folders", func(t *testing.T) {
		srcDir := t.TempDir()
		populateSrcDir(t, srcDir, 0)
		dstDir := t.TempDir()

		dealer := workdir.NewCachedDealer(dstDir, srcDir)

		firstDir, err := dealer.Get("worker-1")
		if err != nil {
			t.Fatal(err)
		}

		dealer.Clean()

		secondDir, err := dealer.Get("worker-1")
		if err != nil {
			t.Fatal(err)
		}

		if firstDir == secondDir {
			t.Errorf("expected manager to be cleaned up")
		}
	})

	t.Run("it works in parallel", func(t *testing.T) {
		srcDir := t.TempDir()
		populateSrcDir(t, srcDir, 0)
		dstDir := t.TempDir()

		dealer := workdir.NewCachedDealer(dstDir, srcDir)
		defer dealer.Clean()

		foldersLock := sync.Mutex{}
		var folders []string

		wg := sync.WaitGroup{}
		wg.Add(10)
		for i := 0; i < 10; i++ {
			i := i
			go func() {
				defer wg.Done()
				f, err := dealer.Get(fmt.Sprintf("test-%d", i))
				if err != nil {
					t.Errorf("unexpected error: %s", err)
				}
				foldersLock.Lock()
				defer foldersLock.Unlock()
				folders = append(folders, f)
			}()
		}

		wg.Wait()

		occurred := make(map[string]bool)
		for _, v := range folders {
			if occurred[v] == true {
				t.Fatal("expected values to be unique")
			}
			occurred[v] = true
		}
	})
}

func TestErrors(t *testing.T) {
	t.Run("dstDir is not a path", func(t *testing.T) {
		srcDir := "not a dir"
		dstDir := t.TempDir()

		dealer := workdir.NewCachedDealer(dstDir, srcDir)

		_, err := dealer.Get("test")
		if err == nil {
			t.Errorf("expected an error")
		}
	})

	t.Run("srcDir is not readable", func(t *testing.T) {
		srcDir := t.TempDir()
		err := os.Chmod(srcDir, 0000)
		clean := os.Chmod
		if runtime.GOOS == "windows" {
			err = acl.Chmod(srcDir, 0000)
			clean = acl.Chmod
		}
		if err != nil {
			t.Fatal(err)
		}
		defer func(d string) {
			_ = clean(d, 0700)
		}(srcDir)

		dstDir := t.TempDir()

		mngr := workdir.NewCachedDealer(dstDir, srcDir)

		_, err = mngr.Get("test")
		if err == nil {
			t.Errorf("expected an error")
		}
	})

	t.Run("dstDir is not writeable", func(t *testing.T) {
		srcDir := t.TempDir()
		dstDir := t.TempDir()
		err := os.Chmod(dstDir, 0000)
		clean := os.Chmod
		if runtime.GOOS == "windows" {
			err = acl.Chmod(dstDir, 0000)
			clean = acl.Chmod
		}
		if err != nil {
			t.Fatal(err)
		}
		defer func(d string) {
			_ = clean(d, 0700)
		}(dstDir)

		dealer := workdir.NewCachedDealer(dstDir, srcDir)

		_, err = dealer.Get("test")
		if err == nil {
			t.Errorf("expected an error")
		}
	})
}

func TestWorkDirReturnsTheRootWorkingDir(t *testing.T) {
	srcDir := t.TempDir()
	populateSrcDir(t, srcDir, 0)
	wdDir := t.TempDir()

	dealer := workdir.NewCachedDealer(wdDir, srcDir)
	defer dealer.Clean()

	rootWorkingDir := dealer.WorkDir()
	if rootWorkingDir != wdDir {
		t.Errorf("expected working dir to be %s, got: %s", wdDir, rootWorkingDir)
	}
}

func BenchmarkDealerGet(b *testing.B) {
	srcDir := b.TempDir()
	populateSrcDir(b, srcDir, 5)

	var wIdx = -1
	var workers = []int{1, 2, 3, 4, 5, 6, 7, 8}

	getNextWorker := func() string {
		wIdx++
		if wIdx > 7 {
			wIdx = 0
		}

		return "worker-" + strconv.Itoa(workers[wIdx])
	}

	for i := 0; i < b.N; i++ {
		runTest(b, srcDir, getNextWorker())
	}
}

func runTest(b *testing.B, srcDir, workerName string) {
	b.Helper()
	dstDir := b.TempDir()

	mngr := workdir.NewCachedDealer(dstDir, srcDir)
	defer mngr.Clean()

	_, err := mngr.Get(workerName)
	if err != nil {
		b.Fatal(err)
	}
}

type testT interface {
	Helper()
	Fatal(args ...any)
}

func populateSrcDir(t testT, srcDir string, depth int) {
	t.Helper()
	if depth == 0 {
		return
	}

	for i := 0; i < 10; i++ {
		dirName := filepath.Join(srcDir, fmt.Sprintf("srcdir-%d", i))
		err := os.Mkdir(dirName, 0700)
		if err != nil {
			t.Fatal(err)
		}
		populateSrcDir(t, dirName, depth-1)
	}

	for i := 0; i < 10; i++ {
		fileName := filepath.Join(srcDir, fmt.Sprintf("srcfile-%d", i))
		err := os.WriteFile(fileName, getFileBytes(), 0400)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func getFileBytes() []byte {
	b, _ := os.ReadFile("testdata/filetocopy_go")

	return b
}
