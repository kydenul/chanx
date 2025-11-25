# GitHub Actions CI/CD

## CI 工作流

本项目使用 GitHub Actions 进行持续集成。

### 工作流说明

**ci.yml** - 主要的持续集成流程

**触发条件：**

- Push 到 `main`、`master` 或 `develop` 分支
- Pull Request 到这些分支

**包含的任务：**

1. **Test** - 测试任务
   - 多平台：Ubuntu、macOS、Windows
   - 多版本：Go 1.23、1.24、1.25
   - 竞态检测：使用 `-race` 标志
   - 代码覆盖率：生成并上传到 Codecov

2. **Lint** - 代码检查
   - 使用 golangci-lint 进行代码质量检查

3. **Benchmark** - 性能测试
   - 运行基准测试并保存结果

4. **Security** - 安全扫描
   - 使用 Gosec 进行安全漏洞扫描

### Go 版本

CI 测试使用以下 Go 版本：

- Go 1.23
- Go 1.24
- Go 1.25

### 本地测试

推送代码前，建议在本地运行以下命令：

```bash
# 运行测试
go test -v -race -coverprofile=coverage.out ./...

# 运行 linter
golangci-lint run --timeout=5m

# 运行基准测试
go test -bench=. -benchmem ./...
```

### Codecov 配置

如需启用代码覆盖率报告：

1. 访问 [codecov.io](https://codecov.io)
2. 使用 GitHub 账号登录
3. 添加此仓库
4. 在仓库设置中添加 `CODECOV_TOKEN` secret（公开仓库可选）

### 徽章

README 中的徽章显示：

- **CI**: 持续集成状态
- **Codecov**: 代码覆盖率
- **License**: MIT 许可证
