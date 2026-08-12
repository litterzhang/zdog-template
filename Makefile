GO      ?= go
PYTHON  ?= python3
UV      ?= uv
PYDIR   := sdk/python
CLIDIR  := cli
SOEXT   := so
ifeq ($(shell uname -s),Darwin)
SOEXT := dylib
endif
LIB := cshared/libztpl.$(SOEXT)

.PHONY: all ci build test bench conformance fmt fmt-check vet alloc clean help py-sync py-test py-build cli-sync cli-test demo gh-setup

all: build test conformance cli-test ## 构建 + 全部测试 + 一致性用例

ci: fmt-check vet test conformance cli-test alloc ## CI 跑的全套（本地可复现）

fmt-check: ## 检查格式（不修改）
	@out=$$(gofmt -l core cshared conformance); \
	if [ -n "$$out" ]; then echo "未格式化:"; echo "$$out"; exit 1; fi
	@echo "gofmt clean"

alloc: ## 零分配门禁 —— 分配次数是确定性的，适合做 CI 硬门禁
	$(GO) test -run Alloc -v ./core/pipeline/

build: $(LIB) ## 构建 C 共享库

$(LIB): $(shell find core cshared -name '*.go' 2>/dev/null)
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -o $(LIB) ./cshared/

fmt: ## 格式化
	gofmt -w core cshared conformance

vet: ## 静态检查
	$(GO) vet ./...

test: vet ## Go 单测（含竞态检测）
	$(GO) test -race ./...

conformance: build ## 跨语言一致性用例：Go 与 Python 跑同一套 cases/*.json
	@echo "--- Go ---"
	@$(GO) test ./conformance/
	@echo "--- Python ---"
	@cd $(PYDIR) && UV_LINK_MODE=copy $(UV) run pytest tests/test_conformance.py

py-sync: ## 同步 Python 开发依赖
	@cd $(PYDIR) && UV_LINK_MODE=copy $(UV) sync

py-test: build ## Python SDK 全部测试
	@cd $(PYDIR) && UV_LINK_MODE=copy $(UV) run pytest

py-build: build ## 构建 Python wheel（先把 .so 拷进包内）
	@cp cshared/libztpl.$(SOEXT) $(PYDIR)/ztemplate/
	# 必须 --wheel：默认的 uv build 会先打 sdist 再从 sdist 建 wheel，
	# 而 .so 是平台产物不进 sdist，那样 wheel 里就没有库了。
	@cd $(PYDIR) && UV_LINK_MODE=copy $(UV) build --wheel

cli-sync: ## 同步 CLI 依赖
	@cd $(CLIDIR) && UV_LINK_MODE=copy $(UV) sync

cli-test: build ## CLI 测试
	@cd $(CLIDIR) && UV_LINK_MODE=copy $(UV) run pytest

gh-setup: ## 建两个发布环境并限制只能从 v* tag 部署
	@repo=$$(gh repo view --json nameWithOwner -q .nameWithOwner); \
	for env in release-sdk release-cli; do \
	  gh api -X PUT "repos/$$repo/environments/$$env" >/dev/null; \
	  gh api -X PUT "repos/$$repo/environments/$$env" --input - >/dev/null <<< \
	    '{"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}'; \
	  gh api -X POST "repos/$$repo/environments/$$env/deployment-branch-policies" \
	    -f name='v*' -f type='tag' >/dev/null 2>&1 || true; \
	  echo "  $$env 就绪（只允许 v* tag 部署）"; \
	done

demo: build ## 跑 CLI 自带的完整例子
	@cd $(CLIDIR) && UV_LINK_MODE=copy $(UV) run ztpl demo

# 迭代数要够大：200x 在这类 ns 级基准上主要测的是预热噪声。
bench: build ## 性能门禁
	@echo "--- Go core (parse) ---"
	@$(GO) test -run=XXX -bench=. -benchtime=1000000x ./core/plan/ \
		| grep -vE '^(goos|goarch|pkg|cpu)'
	@echo "--- Go core (pipeline) ---"
	@$(GO) test -run=XXX -bench=. -benchtime=3000x ./core/pipeline/ \
		| grep -vE '^(goos|goarch|pkg|cpu)'
	@echo "--- Python SDK ---"
	@$(PYTHON) bench/bench_sdk.py

clean: ## 清理构建产物
	rm -f cshared/libztpl.so cshared/libztpl.dylib cshared/libztpl.h
	rm -f $(PYDIR)/ztemplate/libztpl.* 
	rm -rf $(PYDIR)/dist $(PYDIR)/.pytest_cache $(CLIDIR)/dist $(CLIDIR)/.pytest_cache
	find . -name __pycache__ -type d -exec rm -rf {} + 2>/dev/null || true

help: ## 显示可用目标
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
