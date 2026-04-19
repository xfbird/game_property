// property/template.go
package property

import "fmt"

// PropTemplate 属性模板
type PropTemplate struct {
    ID          int
    Name        string
    Description string
    
    // 原始属性ID列表
    PropIDs []int
    
    // 默认值覆盖
    Defaults map[int]float64
    
    // 预计算的映射表（共享）
    globalToLocal map[int]int
    localToGlobal []int
    
    // 扩展后的属性ID列表
    expandedPropIDs []int
}

// GetGlobalToLocal 获取全局到局部的映射
func (t *PropTemplate) GetGlobalToLocal() map[int]int {
    return t.globalToLocal
}

// GetLocalToGlobal 获取局部到全局的映射
func (t *PropTemplate) GetLocalToGlobal() []int {
    return t.localToGlobal
}

// property/template.go
func (t *PropTemplate) buildMaps() {
    n := len(t.expandedPropIDs)
    t.globalToLocal = make(map[int]int, n)
    t.localToGlobal = make([]int, n)
    
    for i, propID := range t.expandedPropIDs {
        // 确保propID有效
        if propID <= 0 {
            panic(fmt.Sprintf("invalid propID %d at index %d", propID, i))
        }
        
        // 确保不重复
        if _, exists := t.globalToLocal[propID]; exists {
            panic(fmt.Sprintf("duplicate propID %d", propID))
        }
        
        t.globalToLocal[propID] = i
        t.localToGlobal[i] = propID
    }
    
    // 验证映射完整性
    if len(t.globalToLocal) != len(t.localToGlobal) {
        panic("globalToLocal and localToGlobal size mismatch")
    }
}