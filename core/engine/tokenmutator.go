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

package engine

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-maxhub/gremlins/core/mutator"
)

// TokenMutator is a mutator.Mutator of a token.Token.
//
// Since the AST is shared among mutants, it is important to avoid that more
// than one mutation is applied to the same file before writing it. For this
// reason, TokenMutator contains a cache of locks, one for each file.
// Every time a mutation is about to being applied, a lock is acquired for
// the file it is operating on. Once the file is written and the token is
// rolled back, the lock is released.
// Keeping a lock per file instead of a lock per TokenMutator allows to apply
// mutations on different files in parallel.
type TokenMutator struct {
	pkg         string
	fs          *token.FileSet
	file        *ast.File
	tokenNode   *NodeToken
	workDir     string
	origFile    []byte
	status      mutator.Status
	mutantType  mutator.Type
	actualToken token.Token
}

// NewTokenMutant initialises a TokenMutator.
func NewTokenMutant(pkg string, set *token.FileSet, file *ast.File, node *NodeToken) *TokenMutator {
	return &TokenMutator{
		pkg:       pkg,
		fs:        set,
		file:      file,
		tokenNode: node,
	}
}

// Type returns the mutator.Type of the mutant.Mutator.
func (m *TokenMutator) Type() mutator.Type {
	return m.mutantType
}

// SetType sets the mutator.Type of the mutant.Mutator.
func (m *TokenMutator) SetType(mt mutator.Type) {
	m.mutantType = mt
}

// Status returns the mutator.Status of the mutant.Mutator.
func (m *TokenMutator) Status() mutator.Status {
	return m.status
}

// SetStatus sets the mutator.Status of the mutant.Mutator.
func (m *TokenMutator) SetStatus(s mutator.Status) {
	m.status = s
}

// Position returns the token.Position where the TokenMutator resides.
func (m *TokenMutator) Position() token.Position {
	return m.fs.Position(m.tokenNode.TokPos)
}

// Pos returns the token.Pos where the TokenMutator resides.
func (m *TokenMutator) Pos() token.Pos {
	return m.tokenNode.TokPos
}

// Pkg returns the package name to which the mutant belongs.
func (m *TokenMutator) Pkg() string {
	return m.pkg
}

// Apply saves the original token.Token of the mutator.Mutator and sets the
// current token from the tokenMutations table.
// Apply overwrites the source code file with the mutated one. It also
// stores the original file in the TokenMutator in order to allow
// Rollback to put it back later.
//
// Apply also puts back the original Token after the mutated file write.
// This is done in order to facilitate the atomicity of the operation,
// avoiding locking in a method and unlocking in another.
func (m *TokenMutator) Apply() error {
	fileLock(m.Position().Filename).Lock()
	defer fileLock(m.Position().Filename).Unlock()

	filename := filepath.Join(m.workDir, m.Position().Filename)
	var err error
	m.origFile, err = os.ReadFile(filename)
	if err != nil {
		return err
	}

	m.actualToken = m.tokenNode.Tok()
	m.tokenNode.SetTok(tokenMutations[m.Type()][m.tokenNode.Tok()])

	if err = m.writeMutatedFile(filename); err != nil {
		return err
	}

	// Rollback here to facilitate the atomicity of the operation.
	m.tokenNode.SetTok(m.actualToken)

	return nil
}

func (m *TokenMutator) writeMutatedFile(filename string) error {
	w := &bytes.Buffer{}
	err := printer.Fprint(w, m.fs, m.file)
	if err != nil {
		return err
	}

	err = os.WriteFile(filename, w.Bytes(), 0600)
	if err != nil {
		return err
	}

	return nil
}

var locks = make(map[string]*sync.Mutex)
var mutex sync.RWMutex

func fileLock(filename string) *sync.Mutex {
	lock, ok := cachedLock(filename)
	if !ok {
		mutex.Lock()
		defer mutex.Unlock()
		lock, ok = locks[filename]
		if !ok {
			lock = &sync.Mutex{}
			locks[filename] = lock

			return lock
		}

		return lock
	}

	return lock
}

func cachedLock(filename string) (*sync.Mutex, bool) {
	mutex.RLock()
	defer mutex.RUnlock()
	lock, ok := locks[filename]

	return lock, ok
}

// Rollback puts back the original file after the test and cleans up the
// TokenMutator to free memory.
func (m *TokenMutator) Rollback() error {
	defer m.resetOrigFile()
	filename := filepath.Join(m.workDir, m.Position().Filename)

	return os.WriteFile(filename, m.origFile, 0600)
}

// SetWorkdir sets the base path on which to Apply and Rollback operations.
//
// By default, TokenMutator will operate on the same source on which the analysis
// was performed. Changing the workdir will prevent the modifications of the
// original files.
func (m *TokenMutator) SetWorkdir(path string) {
	m.workDir = path
}

// Workdir returns the current working dir in which the Mutator will apply its mutations.
func (m *TokenMutator) Workdir() string {
	return m.workDir
}

func (m *TokenMutator) resetOrigFile() {
	var zeroByte []byte
	m.origFile = zeroByte
}
