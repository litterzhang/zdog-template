// Package extension 提供 ${ext|extension=NAME,...} 的扩展注册表。
// 复用自旧原型 template/extension，去掉 init 阶段的日志副作用并加并发保护。
package extension

import (
	"fmt"
	"sync"

	"github.com/huge-zhang/zdog-template/core/template/model"
)

var (
	mu   sync.RWMutex
	pool = map[string]model.ExtensionLoader{}
)

// Put 注册一个扩展。重复注册同名扩展会 panic —— 这是编程错误，应在启动时暴露。
func Put(loader model.ExtensionLoader) {
	mu.Lock()
	defer mu.Unlock()
	name := loader.Name()
	if _, dup := pool[name]; dup {
		panic(fmt.Sprintf("template: extension %q registered twice", name))
	}
	pool[name] = loader
}

// Get 按名取扩展，不存在时返回 nil。
func Get(name string) model.ExtensionLoader {
	mu.RLock()
	defer mu.RUnlock()
	return pool[name]
}

// Names 返回已注册的扩展名，供错误提示使用。
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(pool))
	for n := range pool {
		out = append(out, n)
	}
	return out
}
