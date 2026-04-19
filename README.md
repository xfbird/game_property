# 游戏属性管理系统 (Game Property System)
一个高性能、类型安全的Golang游戏属性管理系统，包含完整的属性计算、事件系统、修改器管理、时间轮到期管理等核心功能。
## 📁 项目结构
    game_property/
    ├── property/           # 属性管理系统核心
    │   ├── def.go         # 属性定义结构
    │   ├── def_table.go   # 属性定义表管理
    │   ├── manager.go     # 属性管理器（主逻辑）
    │   ├── runtime_prop.go # 运行时属性对象
    │   ├── event.go       # 事件定义
    │   ├── event_mgr.go   # 事件管理器
    │   ├── modifier.go    # 修改器系统
    │   ├── formula_mgr.go # 公式管理器
    │   ├── formula_impl.go # 公式实现
    │   ├── template.go    # 属性模板
    │   ├── template_mgr.go # 模板管理器
    │   ├── expiry_manager.go # 到期管理器
    │   ├── timing_wheel.go # 多层时间轮
    │   └── types.go       # 类型定义
    ├── utils/
    │   └── gametime/      # 游戏时间工具
    ├── config/
    │   └── props.json     # 属性配置文件
    ├── main.go            # 主程序（包含完整测试套件）
    └── consts.go          # 常量定义
## 🎯 核心特性
### 1. 三种属性类型
    - **立即型 (Immediate)**：直接设置，无计算公式
    - **标准型 (Standard)**：基础属性，可应用修改器
    - **推导型 (Derived)**：通过公式计算，依赖其他属性
### 2. 完整的依赖传播
    - 自动检测推导属性的依赖关系
    - 属性变更自动传播到依赖属性
    - 支持多层依赖（A→B→C）
### 3. 事件驱动架构
    - 属性变更事件通知
    - 支持全局和特定属性监听器
    - 优先级调度的事件处理器
    - 动态扩容的事件队列
### 4. 高性能修改器系统
    - Flat/Add/Mult三种操作类型
    - 永久和临时修改器
    - 批量修改器应用
    - 基于时间轮的到期管理
### 5. 公式系统
    - 可插拔公式计算器
    - 支持浮点、整数、布尔值类型
    - 预置常用游戏公式
### 6. 模板系统
    - 属性模板快速创建对象
    - 支持模板默认值覆盖
    - 自动扩展依赖属性
## 🚀 快速开始
### 安装
    bash
    git clone <repository>
    cd game_property
    go mod init game_property
### 运行示例
    bash
    运行完整测试套件
    go run main.go
    设置日志级别
    export PROPERTY_LOG_LEVEL=debug
    export LOG_FORMAT=json
    go run main.go
### 基本使用示例
    go
    package main
    import (
    "game_property/property"
    "time"
    )
    func main() {
        // 1. 初始化
        property.InitFormulas()
        // 2. 创建属性定义
        defTable := property.NewPropDefTable()
        defTable.Init([]property.PropDefConfig{
            {
                ID:           1,
                Identifier:   "strength",
                Name:         "力量",
                Type:         property.PropTypeStandard,
                DefaultValue: 10,
                ValueType:    property.ValueTypeFloat32,
            },
            {
                ID:             2,
                Identifier:     "attack",
                Name:           "攻击力",
                Type:           property.PropTypeDerived,
                FormulaName:    "LinearAttack",
                DependsOnIdents: []string{"strength"},
                ValueType:      property.ValueTypeFloat32,
            },
        })
// 3. 创建模板
tmpl := &property.PropTemplate{
    ID:    1,
    Name:  "玩家",
    PropIDs: []int{1, 2},
}
// 4. 创建属性管理器
mgr := property.NewPropertyManager(defTable, tmpl, 1001)
// 5. 获取属性值
str, _ := mgr.GetFloatByID(1)  // 力量
atk, _ := mgr.GetFloatByID(2)  // 攻击力
// 6. 应用修改器
mgr.ApplyPermanentModifier(1, 5.0, property.OpTypeFlat, 
    property.SourceTypeEquip, 1001)
}
## 🔧 配置系统
### 属性定义配置 (config/props.json)
json
[
{
"id": 1,
"identifier": "level",
"name": "等级",
"type": 0,
"default_value": 1,
"value_type": 2
},
{
"id": 2,
"identifier": "strength",
"name": "力量",
"type": 1,
"default_value": 10,
"value_type": 1
},
{
"id": 101,
"identifier": "attack",
"name": "攻击力",
"type": 2,
"formula": "LinearAttack",
"depends_idents": ["strength", "agility"],
"value_type": 1
}
]
### 环境变量配置
bash
日志配置
export PROPERTY_LOG_LEVEL=debug    # debug/info/warn/error
export LOG_FORMAT=custom          # custom/json/text
export LOG_SOURCE=true            # 显示源码位置
export DEBUG=1                    # 调试模式
事件系统
export EVENT_QUEUE_SIZE=1000      # 事件队列大小
## 📊 核心API
### 属性管理
go
// 获取属性值
func (mgr *PropertyManager) GetFloatByID(propID int) (float32, bool)

func (mgr *PropertyManager) GetInt32ByID(propID int) (int32, bool)

func (mgr *PropertyManager) GetBoolByID(propID int) (bool, bool)

// 设置立即型属性

func (mgr *PropertyManager) SetPropFloat(propID int, value float32, sourceID int32) (bool, int32)

func (mgr *PropertyManager) SetPropInt(propID int, value int32, sourceID int32) (bool, int32)

func (mgr *PropertyManager) SetPropBool(propID int, value bool, sourceID int32) (bool, int32)

// 应用修改器

func (mgr *PropertyManager) ApplyModifierByID(

propID int,

value any,
opType property.OpType,

sourceType property.SourceType,

sourceID int32,

duration time.Duration) bool

// 批量修改器

func (mgr *PropertyManager) ApplyBatchModifier(

sourceType property.SourceType,

sourceID int32,

items []property.BatchModifierItem,

duration time.Duration) (int, bool)

### 事件系统
go

// 创建事件管理器

eventMgr := property.NewEventManager(1000)

// 设置全局监听器

eventMgr.SetGlobalListenerFunc(func(event property.PropChangeEvent) {

// 处理属性变更事件

})

// 注册特定属性监听器

eventMgr.RegisterForProp(PROP_STR, func(event property.PropChangeEvent) {

// 处理力量变化

}, 5) // 优先级

### 模板管理
go
// 创建模板管理器
tmplMgr := property.NewTemplateManager(defTable)
// 注册模板
playerTmpl := &property.PropTemplate{
ID:          1,
Name:        "玩家",
PropIDs:     []int{PROP_LEVEL, PROP_STR, PROP_AGI, PROP_STA},
Defaults:    map[int]float64{PROP_STR: 12.0},
}
tmplMgr.RegisterTemplate(playerTmpl)
// 从模板创建属性管理器
player := property.NewPropertyManager(defTable, playerTmpl, 1001)
## ⚙️ 性能特性
### 1. 对象池化
    - RuntimeProp对象池
    - TimedModifier对象池
    - 减少GC压力
### 2. 内存布局优化
    - 16字节对齐的热路径字段
    - 缓存友好的数据结构
    - 紧凑的内存布局
### 3. 高效的时间轮
    - 多层时间轮支持高精度
    - 自动降级机制
    - 高效的过期处理
### 4. 无锁设计
    - 读写锁分离
    - 细粒度锁定
    - 减少竞争
## 🧪 测试套件
    项目包含完整的测试套件：
### 运行所有测试
    bash
    go run main.go
### 测试内容包括：
    1. **基础功能测试** - 创建对象、基本属性
    2. **修改器系统测试** - Flat/Add/Mult操作
    3. **事件系统测试** - 事件触发和监听
    4. **依赖传播测试** - 推导属性自动更新
    5. **时间轮测试** - 到期管理功能
    6. **并发安全测试** - 多协程并发访问
    7. **性能压力测试** - 高并发场景
    8. **内存泄漏测试** - 资源回收
    9. **边界条件测试** - 异常处理
    10. **公式系统测试** - 公式计算正确性
## 📈 性能监控
### 内置统计
    go
    // 获取属性管理器统计
    propagate, markDirty, calcProp, eventFire, eventSkip := mgr.GetStats()
    // 获取事件管理器统计
    eventStats := eventMgr.GetStats()
    // 获取时间轮统计
    timingStats := timingWheel.GetStats()
    // 获取到期管理器统计
    expiryMgr.PrintStats()
### 性能指标
    - 属性计算延迟
    - 事件处理吞吐量
    - 内存使用情况
    - GC暂停时间
## 🔄 扩展开发
### 添加新公式
    go
    // 1. 实现公式接口
    type MyFormula struct{}
    func (f MyFormula) Calculate(vt property.ValueType, props []property.RuntimeProp) uint32 {
        // 计算逻辑
        value1 := property.GetPropFloat(props[0])
        value2 := property.GetPropFloat(props[1])
        result := value1 + value2 * 2
        return vt.FromFloat32ToRaw(result)
    }
    // 2. 在 InitFormulas 中注册
    func InitFormulas() {
        // ...
        globalFormulaMgr.RegisterFormula("MyFormula", &MyFormula{})
    }
### 自定义值类型
    go
    // 扩展 ValueType
    const (
        ValueTypeVector2 ValueType = 4
        ValueTypeVector3 ValueType = 5
    )
    // 实现对应的编解码方法
    func (vt ValueType) FromVector2ToRaw(x, y float32) uint32 {
    // 实现编码逻辑
    }
## 🐛 问题排查
### 常见问题
1. **属性不更新**
   - 检查依赖关系配置
   - 验证公式名称是否正确
   - 查看脏标记传播
2. **事件不触发**
   - 检查事件管理器是否启用
   - 验证是否有监听器注册
   - 查看事件队列状态
3. **修改器不生效**
   - 检查操作类型是否冲突
   - 验证sourceID是否唯一
   - 查看到期时间设置
### 调试日志
        启用调试日志查看详细流程：
        bash
        export PROPERTY_LOG_LEVEL=debug
        export LOG_FORMAT=custom
        go run main.go
## 📄 代码生成
    项目包含常量生成工具，根据配置文件自动生成常量：
    bash
    生成常量文件
    go generate ./...
    生成的常量文件包含所有属性ID的常量定义，便于代码中使用。
## 🤝 贡献指南
    1. Fork 仓库
    2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
    3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
    4. 推送到分支 (`git push origin feature/AmazingFeature`)
    5. 开启 Pull Request
### 代码规范
    - 遵循Go代码规范
    - 添加必要的测试
    - 更新相关文档
    - 保持向后兼容
## 📄 许可证
    MIT License - 详见 [LICENSE](LICENSE) 文件
## 📞 支持
    如有问题或建议，请提交Issue。
---
**项目状态**: ✅ 生产就绪  
**版本**: 1.0.0  
**最后更新**: 2024年1月  
**Go版本要求**: 1.20+
