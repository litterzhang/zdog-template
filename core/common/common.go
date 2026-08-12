// Package common 提供跨包共用的基础类型。
// 复用自旧原型 huge-zhang/ztemplate/common。
package common

// Void 是零尺寸占位类型，用于把 map 当作 set 使用。
type Void struct{}

// VOID 是 Void 的唯一值。
var VOID Void

// EmptyStr 是空字符串常量，避免散落的字面量。
const EmptyStr = ""

// EmptyFunc 是无操作函数。
func EmptyFunc() {}
