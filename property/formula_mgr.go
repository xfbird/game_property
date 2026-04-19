// property/formula_mgr.go
package property

import (
	"log/slog"
	"sync"
)

// FormulaManager 公式管理器接口
type FormulaManager interface {
	GetFormula(name string) (FormulaCalculator, bool)
	RegisterFormula(name string, calculator FormulaCalculator)
}

// formulaManagerImpl 公式管理器实现
type formulaManagerImpl struct {
	mu       sync.RWMutex
	formulas map[string]FormulaCalculator
}

// NewFormulaManager 创建公式管理器
func NewFormulaManager() FormulaManager {
	return &formulaManagerImpl{
		formulas: make(map[string]FormulaCalculator),
	}
}

// GetFormula 获取公式
func (mgr *formulaManagerImpl) GetFormula(name string) (FormulaCalculator, bool) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	
	calculator, ok := mgr.formulas[name]
	return calculator, ok
}

// RegisterFormula 注册公式
func (mgr *formulaManagerImpl) RegisterFormula(name string, calculator FormulaCalculator) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	
	slog.Debug("注册公式",
		"formula_name", name)
	
	mgr.formulas[name] = calculator
}

// 全局公式管理器实例
var globalFormulaMgr FormulaManager
var initFormulaMgrOnce sync.Once

// InitFormulas 初始化公式系统
func InitFormulas() {
	initFormulaMgrOnce.Do(func() {
		globalFormulaMgr = NewFormulaManager()
		
		// 注册公式
		globalFormulaMgr.RegisterFormula("LinearAttack", &LinearAttackFormula{})
		globalFormulaMgr.RegisterFormula("MaxHP", &MaxHPFormula{})
		globalFormulaMgr.RegisterFormula("Defense", &DefenseFormula{})
		globalFormulaMgr.RegisterFormula("CombatPower", &CombatPowerFormula{})
		globalFormulaMgr.RegisterFormula("Sum", &SumFormula{})
		
		slog.Info("公式系统初始化完成",
			"formula_count", 5)
	})
}

// GetFormula 全局获取公式
func GetFormula(name string) (FormulaCalculator, bool) {
	if globalFormulaMgr == nil {
		slog.Error("公式管理器未初始化")
		return nil, false
	}
	return globalFormulaMgr.GetFormula(name)
}