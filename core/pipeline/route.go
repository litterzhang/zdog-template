package pipeline

import (
	"fmt"

	"github.com/huge-zhang/zdog-template/core/plan"
)

// route 描述目标计划的每个槽位从源计划的哪个槽位取值。
//
// 重复块使得路由必须递归：目标的组对应源的组，组内字段再各自对应。
type route struct {
	scalars []int        // 目标标量槽位 -> 源标量槽位
	groups  []groupRoute // 目标组槽位 -> 源组 + 子路由
}

type groupRoute struct {
	srcGroup int
	sub      route
}

// buildRoute 按名字（经 mapping 重映射）把目标计划接到源计划上。
func buildRoute(src, tgt *plan.Plan, mapping map[string]string, path string) (route, error) {
	resolve := func(name string) string {
		if m, ok := mapping[name]; ok {
			return m
		}
		return name // 同名直通
	}

	r := route{scalars: make([]int, tgt.NumSlots())}
	for i, tName := range tgt.Names() {
		sName := resolve(tName)
		slot, ok := src.Slot(sName)
		if !ok {
			return route{}, fmt.Errorf(
				"pipeline: target field %s%q maps to unknown source field %q (available: %v)",
				path, tName, sName, src.Names())
		}
		r.scalars[i] = slot
	}

	for g := 0; g < tgt.NumGroups(); g++ {
		info := tgt.Group(g)
		sName := resolve(info.Name)
		sSlot, ok := src.GroupSlot(sName)
		if !ok {
			return route{}, fmt.Errorf(
				"pipeline: target block %s%q maps to unknown source block %q",
				path, info.Name, sName)
		}
		sub, err := buildRoute(src.Group(sSlot).Sub, info.Sub, mapping,
			path+info.Name+"[].")
		if err != nil {
			return route{}, err
		}
		r.groups = append(r.groups, groupRoute{srcGroup: sSlot, sub: sub})
	}
	return r, nil
}

// fill 按路由把源解析结果搬进目标渲染输入。
//
// 这里就是「零拷贝」发生的地方：目标的每个值都是源文的子切片，
// 直到最后写出才发生一次拷贝。
func (r *route) fill(d *plan.Data, res *plan.Result, src []byte) bool {
	if cap(d.Values) < len(r.scalars) {
		d.Values = make([][]byte, len(r.scalars))
	}
	d.Values = d.Values[:len(r.scalars)]
	for i, srcSlot := range r.scalars {
		sp := res.Abs(res.Spans[srcSlot])
		if !sp.Valid() {
			return false
		}
		d.Values[i] = src[sp.Start:sp.End]
	}

	if cap(d.Groups) < len(r.groups) {
		d.Groups = make([]plan.GroupData, len(r.groups))
	}
	d.Groups = d.Groups[:len(r.groups)]
	for g := range r.groups {
		gr := &r.groups[g]
		items := res.Groups[gr.srcGroup].Items
		d.Groups[g].Items = d.Groups[g].Items[:0]
		for i := range items {
			d.Groups[g].Items = plan.GrowData(d.Groups[g].Items)
			if !gr.sub.fill(&d.Groups[g].Items[len(d.Groups[g].Items)-1], &items[i], src) {
				return false
			}
		}
	}
	return true
}
