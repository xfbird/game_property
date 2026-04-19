// property/template_mgr.go
package property

import (
	"log/slog"
	"sync"
)

// TemplateManager 模板管理器
type TemplateManager struct {
	mu        sync.RWMutex
	templates map[int]*PropTemplate
	defTable  *PropDefTable
}

// NewTemplateManager 创建模板管理器
func NewTemplateManager(defTable *PropDefTable) *TemplateManager {
	return &TemplateManager{
		templates: make(map[int]*PropTemplate),
		defTable:  defTable,
	}
}

// GetDefTable 获取属性定义表
func (tm *TemplateManager) GetDefTable() *PropDefTable {
	return tm.defTable
}

// RegisterTemplate 注册模板
func (tm *TemplateManager) RegisterTemplate(tmpl *PropTemplate) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// 扩展模板，包含所有必需的依赖属性
	expandedProps := tm.expandTemplate(tmpl)

	// 创建扩展后的模板
	expandedTmpl := &PropTemplate{
		ID:          tmpl.ID,
		Name:        tmpl.Name,
		Description: tmpl.Description,
		PropIDs:     tmpl.PropIDs,
		Defaults:    make(map[int]float64), // 初始化Defaults
	}

	// 复制原始默认值
	for propID, value := range tmpl.Defaults {
		expandedTmpl.Defaults[propID] = value
	}

	// 为新增的属性设置默认值
	for _, propID := range expandedProps {
		if _, exists := expandedTmpl.Defaults[propID]; !exists {
			if def, ok := tm.defTable.GetDefByID(propID); ok {
				// 使用全局默认值
				expandedTmpl.Defaults[propID] = def.DefaultValue
			}
		}
	}

	// 设置扩展后的属性列表
	expandedTmpl.expandedPropIDs = expandedProps

	// 构建映射表
	expandedTmpl.buildMaps()

	tm.templates[tmpl.ID] = expandedTmpl

	slog.Info("注册模板",
		"template_id", tmpl.ID,
		"name", tmpl.Name,
		"original_prop_count", len(tmpl.PropIDs),
		"expanded_prop_count", len(expandedProps))
}

// expandTemplate 扩展模板属性
func (tm *TemplateManager) expandTemplate(tmpl *PropTemplate) []int {
	visited := make(map[int]bool)
	result := make([]int, 0, len(tmpl.PropIDs)*2)

	stack := make([]int, len(tmpl.PropIDs))
	copy(stack, tmpl.PropIDs)

	for len(stack) > 0 {
		n := len(stack) - 1
		propID := stack[n]
		stack = stack[:n]

		if visited[propID] {
			continue
		}
		visited[propID] = true
		result = append(result, propID)

		def, ok := tm.defTable.GetDefByID(propID)
		if !ok {
			continue
		}

		if def.Type == PropTypeDerived {
			for _, depID := range def.DependsOn {
				if !visited[depID] {
					stack = append(stack, depID)
				}
			}
		}
	}

	return result
}

// GetTemplate 获取模板
func (tm *TemplateManager) GetTemplate(templateID int) (*PropTemplate, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tmpl, ok := tm.templates[templateID]
	return tmpl, ok
}

// CreateFromTemplate 从模板创建属性管理器
func (tm *TemplateManager) CreateFromTemplate(templateID int, objectID int64) *PropertyManager {
	tmpl, ok := tm.GetTemplate(templateID)
	if !ok {
		return nil
	}

	return NewPropertyManager(tm.defTable, tmpl, objectID)
}