// Package model 定义模板元素的抽象。
// 复用自旧原型 template/model，关键改造见 element.go。
package model

import "github.com/litterzhang/zdog-template/core/common"

// Tag 标识模板元素的种类。
type Tag string

const (
	// TagText 是字面量锚点。裸文本会自动归为此类。
	TagText Tag = "text"
	// TagRegex 是正则洞。`${name}` 语法糖也会归为此类（无 expr 约束）。
	TagRegex Tag = "re"
	// TagJSON 是 JSON 结构化岛。
	TagJSON Tag = "json"
	// TagExt 是扩展点。
	TagExt Tag = "ext"
	// TagEach 开启一个重复块，必须由 TagEnd 闭合。
	TagEach Tag = "each"
	// TagEnd 闭合最近的 TagEach。它是结构标记，不产生元素。
	TagEnd Tag = "end"
)

// SupportedTags 是所有内置 tag 的集合。
var SupportedTags = map[Tag]common.Void{
	TagText:  common.VOID,
	TagRegex: common.VOID,
	TagJSON:  common.VOID,
	TagExt:   common.VOID,
	TagEach:  common.VOID,
	TagEnd:   common.VOID,
}

// Supported 报告 t 是否为受支持的 tag。
func Supported(t Tag) bool {
	_, ok := SupportedTags[t]
	return ok
}
