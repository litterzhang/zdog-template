// Package main 提供 Z-Template 的 C ABI，编译为 libztpl.so。
//
// 设计铁律（见 DESIGN.md §8）：
//  1. 配置即数据 —— Compile 吃一段 JSON，所有语言传同样的字节
//  2. 不透明整数句柄 —— 绝不跨界传结构体或指针
//  3. 调用方分配缓冲 + grow-retry —— 因此不需要 Free()，也就没有跨分配器释放的风险
//  4. 字符串一律 (ptr, len) 字节对，UTF-8，不依赖 NUL 结尾
//  5. 不用回调 —— 各语言 FFI 里最难移植的部分
//  6. 返回后绝不持有宿主内存；句柄内无全局可变状态，可多线程并发调用
//
// 「批量」是架构必需品而非性能优化：逐字段跨界会让 JNA/ffi-napi 这类简单绑定
// 比原生实现还慢；批量之后每行摊到的边界成本是 0.0003 µs，宿主语言只影响 ~8%。
package main

/*
#include <stdint.h>
*/
import "C"

import (
	"sync"
	"unsafe"

	"github.com/huge-zhang/zdog-template/core/pipeline"
)

// AbiVersion 是 ABI 版本。宿主 SDK 必须在加载时校验。
const AbiVersion = 1

// 返回码。n >= 0 表示写入的字节数。
const (
	errShortBuffer = -1 // 输出缓冲不足，stats[2] 为所需容量
	errHandle      = -2 // 无效句柄
	errConfig      = -3 // 配置错误，用 ZtplLastError 取详情
	errArg         = -4 // 参数非法
)

// statsLen 是 stats 数组的长度：[matched, total, needed]。
const statsLen = 3

type handle struct {
	pl  *pipeline.Pipeline
	err string
	// 复用的工作区。句柄可被并发调用，因此走 Pool 而非固定字段。
	pool sync.Pool
}

type work struct {
	scratch *pipeline.Scratch
	buf     []byte
}

func (h *handle) get() *work {
	if w, _ := h.pool.Get().(*work); w != nil {
		return w
	}
	return &work{scratch: h.pl.NewScratch()}
}

func (h *handle) put(w *work) { h.pool.Put(w) }

var (
	regMu sync.RWMutex
	reg   = map[int64]*handle{}
	nextH int64
)

func store(h *handle) int64 {
	regMu.Lock()
	defer regMu.Unlock()
	nextH++
	reg[nextH] = h
	return nextH
}

func lookup(id int64) *handle {
	if id < 0 {
		id = -id
	}
	regMu.RLock()
	defer regMu.RUnlock()
	return reg[id]
}

func bs(p *C.uint8_t, n C.int32_t) []byte {
	if p == nil || n <= 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))
}

func is32(p *C.int32_t, n int) []int32 {
	if p == nil {
		return nil
	}
	return unsafe.Slice((*int32)(unsafe.Pointer(p)), n)
}

//export ZtplAbiVersion
func ZtplAbiVersion() C.int32_t { return AbiVersion }

// ZtplCompile 编译一条流水线配置，返回正数句柄。
// 失败时返回负数句柄 —— 仍可用它调用 ZtplLastError 取错误详情，
// 之后必须用 ZtplRelease 释放。
//
//export ZtplCompile
func ZtplCompile(cfgPtr *C.uint8_t, cfgLen C.int32_t) C.int64_t {
	data := bs(cfgPtr, cfgLen)
	if data == nil {
		return C.int64_t(errArg)
	}
	cfg, err := pipeline.ParseConfig(data)
	if err == nil {
		var pl *pipeline.Pipeline
		if pl, err = pipeline.Compile(cfg); err == nil {
			return C.int64_t(store(&handle{pl: pl}))
		}
	}
	return C.int64_t(-store(&handle{err: err.Error()}))
}

// ZtplTransform 批量转换。
//
// stats 必须指向一个长度 >= 3 的 int32 数组，返回 [matched, total, needed]。
// 缓冲不足时返回 errShortBuffer，此时 stats[2] 为所需容量，调用方扩容后重试。
//
//export ZtplTransform
func ZtplTransform(id C.int64_t, inPtr *C.uint8_t, inLen C.int32_t,
	outPtr *C.uint8_t, outCap C.int32_t, statsPtr *C.int32_t) C.int32_t {

	h := lookup(int64(id))
	if h == nil || h.pl == nil {
		return C.int32_t(errHandle)
	}
	stats := is32(statsPtr, statsLen)
	if stats == nil {
		return C.int32_t(errArg)
	}
	in := bs(inPtr, inLen)

	w := h.get()
	defer h.put(w)

	out, matched, total := h.pl.Transform(w.buf[:0], in, w.scratch)
	w.buf = out // 保留扩容后的容量供后续复用

	stats[0], stats[1], stats[2] = int32(matched), int32(total), int32(len(out))
	if len(out) > int(outCap) {
		return C.int32_t(errShortBuffer)
	}
	if len(out) > 0 {
		copy(bs(outPtr, outCap), out)
	}
	return C.int32_t(len(out))
}

// ZtplLastError 取出句柄上的最近一次错误信息。
// 缓冲不足时返回负的所需长度。
//
//export ZtplLastError
func ZtplLastError(id C.int64_t, outPtr *C.uint8_t, outCap C.int32_t) C.int32_t {
	h := lookup(int64(id))
	if h == nil {
		return C.int32_t(errHandle)
	}
	msg := []byte(h.err)
	if len(msg) == 0 {
		return 0
	}
	dst := bs(outPtr, outCap)
	if len(msg) > len(dst) {
		return C.int32_t(-len(msg))
	}
	return C.int32_t(copy(dst, msg))
}

//export ZtplRelease
func ZtplRelease(id C.int64_t) {
	k := int64(id)
	if k < 0 {
		k = -k
	}
	regMu.Lock()
	delete(reg, k)
	regMu.Unlock()
}

func main() {}
