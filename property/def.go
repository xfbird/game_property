// property/def.go
package property

// PropDefConfig 属性定义配置
type PropDefConfig struct {
	ID         int      `json:"id"`
	Identifier string   `json:"identifier"`
	Name       string   `json:"name"`
	Type       PropType `json:"type"`

	// 默认值：当没有任何修改器时返回的值
	DefaultValue float64 `json:"default_value"`

	// 值类型
	ValueType ValueType `json:"value_type,omitempty"`

	FormulaName string `json:"formula,omitempty"`

	// 运行时使用的数字ID依赖
	DependsOn []int `json:"depends,omitempty"`

	// 配置文件中使用的标识符依赖
	DependsOnIdents []string `json:"depends_idents,omitempty"`
}
