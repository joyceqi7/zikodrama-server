# 开发协议

1. 阅读 `AGENTS.md`、`CONTEXT.md` 和 `docs/api-contract.md`。
2. 先增加能够复现问题的领域或网络接口回归测试。
3. 通过领域接口和适配器实现变更。
4. 编辑期间运行聚焦包测试。
5. 提交前运行 `make verify` 和 `make build`。
6. 复查密钥、授权、幂等、速率限制和合同兼容性。
7. 双通道变更保持消费者旧路径和官方字段，管理路由固定通道；同时验证新官方与独立 Mini，不用当前源码测试替代实际旧包验收。发布与清理遵循 `docs/veplayer-release-runbook.md`。
