
Game Property Management System

    https://img.shields.io/badge/go-1.20+-00ADD8?style=for-the-badge&logo=go
    https://img.shields.io/badge/License-MIT-yellow.svg?style=for-the-badge
    https://img.shields.io/github/issues/yourusername/game-property?style=for-the-badge
    https://img.shields.io/github/stars/yourusername/game-property?style=for-the-badge

一个高性能、类型安全的Golang游戏属性管理系统，专为大型在线游戏设计。支持属性计算、事件驱动、修改器系统、公式推导和时间管理。

✨ 特性亮点
🎯 核心功能
    三种属性类型：立即型、标准型、推导型
    完整的依赖传播：自动计算推导属性
    事件驱动架构：属性变更事件通知
    多层时间轮：高精度到期管理
    批量操作：支持批量修改器应用
⚡ 性能优化
    零GC设计：对象池化减少垃圾回收
    内存对齐：16字节对齐的热路径字段
    无锁设计：读写锁分离减少竞争
    缓存友好：优化内存布局提高缓存命中率
🔧 可扩展性
    公式系统：可插拔公式计算器
    模板系统：属性模板快速创建
    动态配置：JSON配置文件驱动
    多值类型：支持float32/int32/bool统一存储
📦 快速开始
    安装
        bash
        go get github.com/yourusername/game-property
    基本使用
        package main

        import (
            "github.com/yourusername/game-property/property"
            "time"
        )

        func main() {
            // 1. 初始化公式系统
            property.InitFormulas()
            
            // 2. 加载属性定义
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
            
            // 3. 创建属性管理器
            tmpl := &property.PropTemplate{
                ID:    1,
                Name:  "玩家模板",
                PropIDs: []int{1, 2},
            }
            
            player := property.NewPropertyManager(defTable, tmpl, 1001)
            
            // 4. 获取属性值
            attack, _ := player.GetFloatByID(2)
            fmt.Printf("初始攻击力: %.1f\n", attack)
            
            // 5. 应用修改器
            player.ApplyPermanentModifier(1, 5.0, property.OpTypeFlat, 
                property.SourceTypeEquip, 5001)
            
            attack, _ = player.GetFloatByID(2)
            fmt.Printf("加装后的攻击力: %.1f\n", attack)
            
            // 6. 应用临时Buff
            player.ApplyModifierByID(1, 0.2, property.OpTypePercentAdd,
                property.SourceTypeBuff, 6001, 30*time.Second)
        }
🏗️ 系统架构
    核心组件
        game-property/
        ├── property/           # 属性管理核心
        │   ├── def_table.go    # 属性定义表
        │   ├── manager.go      # 属性管理器
        │   ├── runtime_prop.go # 运行时属性
        │   ├── event_mgr.go    # 事件管理器
        │   ├── modifier.go     # 修改器系统
        │   ├── formula_mgr.go  # 公式管理器
        │   ├── template.go     # 模板系统
        │   ├── expiry_manager.go # 到期管理器
        │   ├── timing_wheel.go # 多层时间轮
        │   └── types.go        # 类型定义
        ├── utils/
        │   └── gametime/       # 游戏时间工具
        └── examples/           # 示例代码
    数据流
        📊 性能指标
        基准测试结果
        bash
            # 运行基准测试
            go test -bench=. -benchmem ./property/

            # 示例输出
            BenchmarkPropertyGet-8          5000000    285 ns/op    0 B/op    0 allocs/op
            BenchmarkPropertySet-8          2000000    712 ns/op    0 B/op    0 allocs/op
            BenchmarkModifierApply-8        1000000   1420 ns/op   16 B/op    1 allocs/op
            BenchmarkEventTrigger-8         3000000    498 ns/op    0 B/op    0 allocs/op
            BenchmarkBatchUpdate-8          5000000    325 ns/op    0 B/op    0 allocs/op
        内存使用
            单个属性：48字节
            单个修改器：40字节
            对象池大小：可配置
            零GC压力：对象复用率>95%
🚀 高级特性
    自定义公式
        // 1. 实现公式接口
        type CriticalDamageFormula struct{}

        func (f *CriticalDamageFormula) Calculate(vt property.ValueType, 
            props []*property.RuntimeProp) uint32 {
            attack := property.GetPropFloat(props[0])
            critRate := property.GetPropFloat(props[1])
            critDamage := property.GetPropFloat(props[2])
            
            // 暴击伤害公式
            expectedDamage := attack * (1 + critRate * critDamage)
            return vt.FromFloat32ToRaw(expectedDamage)
        }

    // 2. 注册公式
    property.RegisterFormula("CriticalDamage", &CriticalDamageFormula{})
    批量操作
        // 批量应用装备效果
        items := []property.BatchModifierItem{
            {PropID: 1, Value: float32(5.0), OpType: property.OpTypeFlat},    // 力量+5
            {PropID: 2, Value: float32(3.0), OpType: property.OpTypeFlat},    // 敏捷+3
            {PropID: 3, Value: float32(0.1), OpType: property.OpTypePercentAdd}, // 攻击+10%
        }

        successCount, allSuccess := player.ApplyBatchModifier(
            property.SourceTypeEquip,
            9001,
            items,
            time.Duration(-1),
        )
    事件监听
        // 创建事件管理器
        eventMgr := property.NewEventManager(1000)

        // 全局监听
        eventMgr.SetGlobalListenerFunc(func(event property.PropChangeEvent) {
            slog.Info("属性变更",
                "object_id", event.ObjectID,
                "prop_id", event.PropID,
                "old_value", event.OldValue,
                "new_value", event.NewValue)
        })

        // 特定属性监听
        eventMgr.RegisterForProp(PROP_HP, func(event property.PropChangeEvent) {
            newHP := property.DecodeRawToFloat32(event.NewValue, event.TypeForValue)
            if newHP <= 0 {
                slog.Info("玩家死亡", "object_id", event.ObjectID)
            }
        }, 5)
📈 性能调优
    配置选项
        // 事件管理器配置
        eventMgr := property.NewEventManager(property.EventManagerConfig{
            QueueSize:     10000,    // 队列大小
            WorkerCount:   4,        // 工作协程数
            BatchSize:     100,      // 批处理大小
            EnableMetrics: true,     // 启用指标
        })

        // 时间轮配置
        timingWheel := property.NewTimingWheel(property.TimingWheelConfig{
            Tick:      10 * time.Millisecond,  // 基础tick
            WheelSize: 120,                    // 槽数
            MaxLevel:  4,                      // 最大层数
            Name:      "global",               // 名称
        })
    监控指标
        // 获取性能统计
        stats := player.GetStats()
        fmt.Printf("属性计算次数: %d\n", stats.CalcCount)
        fmt.Printf("事件触发次数: %d\n", stats.EventCount)
        fmt.Printf("传播调用次数: %d\n", stats.PropagateCount)

        // 获取内存统计
        memStats := runtime.GetMemoryStats()
        fmt.Printf("对象池使用率: %.1f%%\n", memStats.PoolUtilization*100)
        fmt.Printf("GC暂停时间: %v\n", memStats.GCPause)
🧪 测试覆盖
    单元测试
        项目 根目录 的 main.go 是一个简单的测试。
🔧 开发指南
    项目结构
        .
        ├── cmd/
        │   └── example/          # 示例程序
        ├── property/             # 核心库
        ├── utils/               # 工具函数
        ├── docs/                # 文档
        ├── scripts/             # 构建脚本
        ├── tests/               # 测试文件
        └── examples/            # 使用示例
    构建
        # 构建库
        go build ./property/

        # 构建示例
        go build ./cmd/example/

        # 代码检查
        golangci-lint run
    代码生成
        # 生成常量文件
        go generate ./property/

        # 更新文档
        go run scripts/docs/generate.go
📖 API文档
    核心接口
    // 属性管理器接口
    type PropertyManager interface {
        GetFloatByID(propID int) (float32, bool)
        GetInt32ByID(propID int) (int32, bool)
        GetBoolByID(propID int) (bool, bool)
        
        ApplyModifierByID(propID int, value any, opType OpType, 
            sourceType SourceType, sourceID int32, duration time.Duration) bool
        ApplyPermanentModifier(propID int, value any, opType OpType,
            sourceType SourceType, sourceID int32) bool
        ApplyBatchModifier(sourceType SourceType, sourceID int32,
            items []BatchModifierItem, duration time.Duration) (int, bool)
        
        RemoveModifiersBySource(sourceType SourceType, sourceID int32) int
        GetModifierInfo(propID int) (flat, add, mult int, hasModifiers bool)
        
        SetEventManager(eventMgr *EventManager)
        Destroy()
    }

