// property/def_table.go
package property

import (
	"fmt"
	"log/slog"
	"sync"
)

// PropDefTable 属性定义表
type PropDefTable struct {
	mu sync.RWMutex

	// 所有属性定义
	defs []PropDefConfig

	// 索引
	idToIndex map[int]int
	identToID map[string]int
	nameToID  map[string]int
}

// NewPropDefTable 创建属性定义表
func NewPropDefTable() *PropDefTable {
	return &PropDefTable{
		defs:      make([]PropDefConfig, 0),
		idToIndex: make(map[int]int),
		identToID: make(map[string]int),
		nameToID:  make(map[string]int),
	}
}

// Init 初始化
func (t *PropDefTable) Init(configs []PropDefConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := len(configs)

	// 1. 深拷贝配置
	t.defs = make([]PropDefConfig, n)
	copy(t.defs, configs)

	// 2. 构建标识符到ID的映射
	t.idToIndex = make(map[int]int)
	t.identToID = make(map[string]int)
	t.nameToID = make(map[string]int)

	for i, def := range t.defs {
		t.idToIndex[def.ID] = i
		t.nameToID[def.Name] = def.ID
		if def.Identifier != "" {
			t.identToID[def.Identifier] = def.ID
		}
	}

	// 3. 转换依赖标识符为数字ID
	for i := range t.defs {
		if err := t.resolveDependencies(i); err != nil {
			return fmt.Errorf("解析属性%d的依赖失败: %v", t.defs[i].ID, err)
		}
	}

	// 4. 检测循环依赖
	if err := t.detectCircularDependencies(); err != nil {
		return fmt.Errorf("检测到循环依赖: %v", err)
	}

	slog.Info("属性定义表初始化完成",
		"prop_count", n)
	
	return nil
}

// resolveDependencies 解析依赖标识符
func (t *PropDefTable) resolveDependencies(idx int) error {
	def := &t.defs[idx]

	// 如果已经设置了DependsOn，则不需要转换
	if len(def.DependsOn) > 0 {
		return nil
	}

	// 转换DependsOnIdents为DependsOn
	if len(def.DependsOnIdents) > 0 {
		def.DependsOn = make([]int, 0, len(def.DependsOnIdents))

		for _, ident := range def.DependsOnIdents {
			depID, ok := t.identToID[ident]
			if !ok {
				slog.Error("解析依赖标识符失败",
					"prop_id", def.ID,
					"prop_name", def.Name,
					"dep_ident", ident)
				return fmt.Errorf("属性%d依赖未知的属性标识符: %s", def.ID, ident)
			}
			def.DependsOn = append(def.DependsOn, depID)
		}

		// 清空标识符列表
		def.DependsOnIdents = nil
	}

	return nil
}

// detectCircularDependencies 检测循环依赖
func (t *PropDefTable) detectCircularDependencies() error {
	// 只检测推导型属性的循环依赖
	derivedProps := make(map[int]bool)
	adj := make(map[int][]int) // 邻接表
	
	// 构建邻接表
	for _, def := range t.defs {
		if def.Type == PropTypeDerived {
			derivedProps[def.ID] = true
			if len(def.DependsOn) > 0 {
				adj[def.ID] = def.DependsOn
			} else {
				adj[def.ID] = []int{} // 没有依赖
			}
		}
	}
	
	// 如果没有推导属性，直接返回
	if len(derivedProps) == 0 {
		return nil
	}
	
	// 对每个推导属性进行DFS检测
	visited := make(map[int]bool)
	inStack := make(map[int]bool)
	
	for propID := range derivedProps {
		if visited[propID] {
			continue
		}
		
		path := make([]int, 0, 10)
		if t.dfsDetectCycle(propID, adj, visited, inStack, &path) {
			// 构建详细的错误信息
			cyclePath := t.buildCyclePathString(path)
			slog.Error("检测到循环依赖",
				"cycle_path", cyclePath)
			return fmt.Errorf("发现循环依赖: %s", cyclePath)
		}
	}
	
	slog.Debug("循环依赖检测完成，未发现循环依赖")
	return nil
}

// dfsDetectCycle DFS检测循环依赖
func (t *PropDefTable) dfsDetectCycle(node int, adj map[int][]int, 
    visited, inStack map[int]bool, path *[]int) bool {
    visited[node] = true
    inStack[node] = true
    *path = append(*path, node)
    
    for _, neighbor := range adj[node] {
        if !visited[neighbor] {
            if t.dfsDetectCycle(neighbor, adj, visited, inStack, path) {
                return true
            }
        } else if inStack[neighbor] {
            // 找到循环，不重复添加起点
            return true
        }
    }
    
    inStack[node] = false
    *path = (*path)[:len(*path)-1]  // 回溯
    return false
}
// buildCyclePathString 构建循环路径字符串
func (t *PropDefTable) buildCyclePathString(path []int) string {
	if len(path) == 0 {
		return ""
	}
	
	// 找到循环的开始位置
	lastNode := path[len(path)-1]
	cycleStart := -1
	for i := 0; i < len(path)-1; i++ {
		if path[i] == lastNode {
			cycleStart = i
			break
		}
	}
	
	if cycleStart == -1 {
		// 不应该发生，但做防御
		return fmt.Sprintf("路径: %v", path)
	}
	
	// 只取循环部分
	cyclePath := path[cycleStart:]
	
	// 构建可读的字符串
	result := ""
	for i, id := range cyclePath {
		if i > 0 {
			result += " -> "
		}
		// 获取属性名称
		name := "未知"
		if def, ok := t.GetDefByID(id); ok {
			name = def.Name
			if def.Identifier != "" {
				name = fmt.Sprintf("%s[%s]", name, def.Identifier)
			}
		}
		result += fmt.Sprintf("%d(%s)", id, name)
	}
	
	return result
}

// GetDefByID 通过ID获取定义
func (t *PropDefTable) GetDefByID(id int) (*PropDefConfig, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	idx, ok := t.idToIndex[id]
	if !ok {
		slog.Debug("通过ID获取定义失败",
			"prop_id", id)
		return nil, false
	}
	return &t.defs[idx], true
}

// GetIDByIdent 通过标识符获取ID
func (t *PropDefTable) GetIDByIdent(ident string) (int, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	id, ok := t.identToID[ident]
	if !ok {
		slog.Debug("通过标识符获取ID失败",
			"ident", ident)
	}
	return id, ok
}

// GetIdentByID 通过ID获取标识符
func (t *PropDefTable) GetIdentByID(id int) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	idx, ok := t.idToIndex[id]
	if !ok {
		slog.Debug("通过ID获取标识符失败",
			"prop_id", id)
		return "", false
	}

	def := t.defs[idx]
	return def.Identifier, def.Identifier != ""
}

// GetNameByID 通过ID获取属性名
func (t *PropDefTable) GetNameByID(id int) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	idx, ok := t.idToIndex[id]
	if !ok {
		slog.Debug("通过ID获取属性名失败",
			"prop_id", id)
		return "", false
	}
	return t.defs[idx].Name, true
}

// GetIDByName 通过名称获取ID
func (t *PropDefTable) GetIDByName(name string) (int, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	id, ok := t.nameToID[name]
	if !ok {
		slog.Debug("通过名称获取ID失败",
			"name", name)
	}
	return id, ok
}

// GetAllDefs 获取所有定义
func (t *PropDefTable) GetAllDefs() []PropDefConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]PropDefConfig, len(t.defs))
	copy(result, t.defs)
	return result
}

// GetDependentProps 获取依赖特定属性的所有推导属性
func (t *PropDefTable) GetDependentProps(propID int) []int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]int, 0)
	for _, def := range t.defs {
		if def.Type == PropTypeDerived {
			for _, depID := range def.DependsOn {
				if depID == propID {
					result = append(result, def.ID)
					break
				}
			}
		}
	}
	return result
}

// GetPropType 获取属性类型
func (t *PropDefTable) GetPropType(propID int) (PropType, bool) {
	def, ok := t.GetDefByID(propID)
	if !ok {
		return PropTypeImmediate, false
	}
	return def.Type, true
}

// GetDefaultValue 获取默认值
func (t *PropDefTable) GetDefaultValue(propID int) (float64, bool) {
	def, ok := t.GetDefByID(propID)
	if !ok {
		return 0, false
	}
	return def.DefaultValue, true
}

// GetValueType 获取值类型
func (t *PropDefTable) GetValueType(propID int) (ValueType, bool) {
	def, ok := t.GetDefByID(propID)
	if !ok {
		return ValueTypeFloat32, false
	}
	return def.ValueType, true
}

// GetFormulaName 获取公式名称
func (t *PropDefTable) GetFormulaName(propID int) (string, bool) {
	def, ok := t.GetDefByID(propID)
	if !ok {
		return "", false
	}
	return def.FormulaName, def.FormulaName != ""
}

// GetDependencies 获取依赖属性
func (t *PropDefTable) GetDependencies(propID int) ([]int, bool) {
	def, ok := t.GetDefByID(propID)
	if !ok {
		return nil, false
	}
	return def.DependsOn, true
}

// HasDependency 检查属性是否有依赖
func (t *PropDefTable) HasDependency(propID int) bool {
	deps, ok := t.GetDependencies(propID)
	return ok && len(deps) > 0
}

// Size 获取定义数量
func (t *PropDefTable) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.defs)
}

// PrintDebugInfo 打印调试信息
func (t *PropDefTable) PrintDebugInfo() {
	t.mu.RLock()
	defer t.mu.RUnlock()

	slog.Info("=== 属性定义表调试信息 ===")
	slog.Info("总属性数", "count", len(t.defs))
	
	for _, def := range t.defs {
		fields := []any{
			"id", def.ID,
			"name", def.Name,
			"type", def.Type.String(),
		}
		
		if def.Identifier != "" {
			fields = append(fields, "ident", def.Identifier)
		}
		if def.Type == PropTypeDerived && len(def.DependsOn) > 0 {
			fields = append(fields, "deps", def.DependsOn)
		}
		
		slog.Info("属性定义", fields...)
	}
}