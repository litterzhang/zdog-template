// Package model 定义 shape（类型系统）的核心抽象。
// 复用自旧原型 shape/model，补充 codec 所需的元信息。
package model

import "github.com/huge-zhang/zdog-template/core/common"

// Type 是 shape 节点的类型标签。
type Type string

const (
	TypeObject Type = "object"
	TypeArray  Type = "array"
	TypeDict   Type = "dict"
	TypeNumber Type = "number"
	TypeString Type = "string"
	TypeBool   Type = "bool"
	TypeAny    Type = "any"
)

// SupportedTypes 是所有内置类型的集合。
var SupportedTypes = map[Type]common.Void{
	TypeObject: common.VOID,
	TypeArray:  common.VOID,
	TypeDict:   common.VOID,
	TypeNumber: common.VOID,
	TypeString: common.VOID,
	TypeBool:   common.VOID,
	TypeAny:    common.VOID,
}

// Supported 报告 t 是否为受支持的类型。
func Supported(t Type) bool {
	_, ok := SupportedTypes[t]
	return ok
}
