// Package loader 把 ${tag|args} 构造成 Element，并提供 tag -> loader 注册表。
// 复用自旧原型 template/loader 的注册表架构。
package loader

import (
	"fmt"
	"sort"
	"sync"

	"github.com/litterzhang/zdog-template/core/template/model"
)

var (
	mu   sync.RWMutex
	pool = map[model.Tag]model.ElementLoader{}
)

// Put 注册某个 tag 的加载器。重复注册会 panic。
func Put(l model.ElementLoader) {
	mu.Lock()
	defer mu.Unlock()
	tag := l.Tag()
	if _, dup := pool[tag]; dup {
		panic(fmt.Sprintf("template: loader for tag %q registered twice", tag))
	}
	pool[tag] = l
}

// Get 按 tag 取加载器，不存在时返回 nil。
func Get(tag model.Tag) model.ElementLoader {
	mu.RLock()
	defer mu.RUnlock()
	return pool[tag]
}

// Tags 返回已注册的 tag（已排序），供错误提示使用。
func Tags() []model.Tag {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]model.Tag, 0, len(pool))
	for t := range pool {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
