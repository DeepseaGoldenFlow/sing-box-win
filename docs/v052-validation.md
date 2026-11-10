# v0.5.2 组与路由验证

2026-11-10。

问题：组列表枚举 Clash API 的全部含 all 字段的对象，导致兼容接口合成的 GLOBAL（Fallback）也显示，并按名称排序。当前订阅原本只定义「🚀 节点代理」Selector 和「♻️ 自动选择」URLTest，两组以外的 GLOBAL 不属于原始配置。

修改：从启用配置的 outbounds 中确定策略组及顺序，再关联运行时成员/选择/延迟；显式定义为 GLOBAL 的真实组不隐藏。新增 /api/routing 读取内核 /configs 的实际 mode，结合原配置显示规则数、默认出口。界面说明组选择与路由规则的关系。订阅与路由规则不变。

验证：单元测试覆盖虚拟 GLOBAL 排除、原配置顺序、真实 GLOBAL 保留、Selector/URLTest 可选择性、实时 mode 读取。Go 测试、Linux/Windows vet、Windows Go 构建、WinUI Release/x64 构建通过。

安装 v0.5.2 后，Win11 实机返回 mode=Rule、rules_count=10、final=🚀 节点代理；组仅包含节点代理与自动选择。服务 running=true、system_proxy=true、error 为空；Google 检查 HTTP 204。此前版本实际 mode 没有独立采样，不能仅凭旧 GLOBAL 标题断言它当时处于全局模式。
