// Copyright 2024 TiKV Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package art

import (
	"unsafe"

	"github.com/tikv/client-go/v2/internal/unionstore/arena"
)

// fixedSizeArena is a fixed size arena allocator.
// because the size of each type of node is fixed, the discarded nodes can be reused.
// reusing blocks reduces the memory pieces.
type nodeArena struct {
	arena.MemdbArena
	freeNode4  []arena.MemdbArenaAddr
	freeNode16 []arena.MemdbArenaAddr
	freeNode48 []arena.MemdbArenaAddr
}

type artAllocator struct {
	vlogAllocator arena.MemdbVlog[*artLeaf, *ART]
	nodeAllocator nodeArena
}

// init the allocator.
func (f *artAllocator) init() {
	f.nodeAllocator.freeNode4 = make([]arena.MemdbArenaAddr, 0, 1<<4)
	f.nodeAllocator.freeNode16 = make([]arena.MemdbArenaAddr, 0, 1<<3)
	f.nodeAllocator.freeNode48 = make([]arena.MemdbArenaAddr, 0, 1<<2)
}

func (f *artAllocator) allocNode4() (arena.MemdbArenaAddr, *node4) {
	var (
		addr arena.MemdbArenaAddr
		data []byte
	)
	if len(f.nodeAllocator.freeNode4) > 0 {
		addr = f.nodeAllocator.freeNode4[len(f.nodeAllocator.freeNode4)-1]
		f.nodeAllocator.freeNode4 = f.nodeAllocator.freeNode4[:len(f.nodeAllocator.freeNode4)-1]
		data = f.nodeAllocator.GetData(addr)
	} else {
		addr, data = f.nodeAllocator.Alloc(node4size, true)
	}
	n4 := (*node4)(unsafe.Pointer(&data[0]))
	n4.init()
	return addr, n4
}

func (f *artAllocator) freeNode4(addr arena.MemdbArenaAddr) {
	f.nodeAllocator.freeNode4 = append(f.nodeAllocator.freeNode4, addr)
}

func (f *artAllocator) getNode4(addr arena.MemdbArenaAddr) *node4 {
	data := f.nodeAllocator.GetData(addr)
	return (*node4)(unsafe.Pointer(&data[0]))
}

func (f *artAllocator) allocNode16() (arena.MemdbArenaAddr, *node16) {
	var (
		addr arena.MemdbArenaAddr
		data []byte
	)
	if len(f.nodeAllocator.freeNode16) > 0 {
		addr = f.nodeAllocator.freeNode16[len(f.nodeAllocator.freeNode16)-1]
		f.nodeAllocator.freeNode16 = f.nodeAllocator.freeNode16[:len(f.nodeAllocator.freeNode16)-1]
		data = f.nodeAllocator.GetData(addr)
	} else {
		addr, data = f.nodeAllocator.Alloc(node16size, true)
	}
	n16 := (*node16)(unsafe.Pointer(&data[0]))
	n16.init()
	return addr, n16
}

func (f *artAllocator) freeNode16(addr arena.MemdbArenaAddr) {
	f.nodeAllocator.freeNode16 = append(f.nodeAllocator.freeNode16, addr)
}

func (f *artAllocator) getNode16(addr arena.MemdbArenaAddr) *node16 {
	data := f.nodeAllocator.GetData(addr)
	return (*node16)(unsafe.Pointer(&data[0]))
}

func (f *artAllocator) allocNode48() (arena.MemdbArenaAddr, *node48) {
	var (
		addr arena.MemdbArenaAddr
		data []byte
	)
	if len(f.nodeAllocator.freeNode48) > 0 {
		addr = f.nodeAllocator.freeNode48[len(f.nodeAllocator.freeNode48)-1]
		f.nodeAllocator.freeNode48 = f.nodeAllocator.freeNode48[:len(f.nodeAllocator.freeNode48)-1]
		data = f.nodeAllocator.GetData(addr)
	} else {
		addr, data = f.nodeAllocator.Alloc(node48size, true)
	}
	n48 := (*node48)(unsafe.Pointer(&data[0]))
	n48.init()
	return addr, n48
}

func (f *artAllocator) freeNode48(addr arena.MemdbArenaAddr) {
	f.nodeAllocator.freeNode48 = append(f.nodeAllocator.freeNode48, addr)
}

func (f *artAllocator) getNode48(addr arena.MemdbArenaAddr) *node48 {
	data := f.nodeAllocator.GetData(addr)
	return (*node48)(unsafe.Pointer(&data[0]))
}

func (f *artAllocator) allocNode256() (arena.MemdbArenaAddr, *node256) {
	var (
		addr arena.MemdbArenaAddr
		data []byte
	)
	addr, data = f.nodeAllocator.Alloc(node256size, true)
	n256 := (*node256)(unsafe.Pointer(&data[0]))
	n256.init()
	return addr, n256
}

func (f *artAllocator) getNode256(addr arena.MemdbArenaAddr) *node256 {
	data := f.nodeAllocator.GetData(addr)
	return (*node256)(unsafe.Pointer(&data[0]))
}

func (f *artAllocator) allocLeaf(key []byte) (arena.MemdbArenaAddr, *artLeaf) {
	size := leafSize + len(key)
	addr, data := f.nodeAllocator.Alloc(size, true)
	lf := (*artLeaf)(unsafe.Pointer(&data[0]))
	lf.klen = uint16(len(key))
	lf.flags = 0
	lf.vAddr = arena.NullAddr
	copy(data[leafSize:], key)
	return addr, lf
}

func (f *artAllocator) getLeaf(addr arena.MemdbArenaAddr) *artLeaf {
	if addr.IsNull() {
		return nil
	}
	data := f.nodeAllocator.GetData(addr)
	return (*artLeaf)(unsafe.Pointer(&data[0]))
}