GO      ?= go
PYTHON  ?= python3
UV      ?= uv
PYDIR   := sdk/python
SOEXT   := so
ifeq ($(shell uname -s),Darwin)
SOEXT := dylib
endif
LIB := cshared/libztpl.$(SOEXT)

.PHONY: all build test bench conformance fmt vet clean help py-sync py-test py-build

all: build test conformance ## 构建 + 测试 + 一致性用例

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
	@cp cshared/libztpl.$(SOEXT) $(PYDIR)/ztpl/
	@cd $(PYDIR) && UV_LINK_MODE=copy $(UV) build

bench: build ## 性能门禁
	@echo "--- Go core ---"
	@$(GO) test -run=XXX -bench=. -benchtime=200x ./core/plan/ ./core/pipeline/ \
		| grep -vE '^(goos|goarch|pkg|cpu)'
	@echo "\n--- Python SDK ---"
	@$(PYTHON) bench/bench_sdk.py

clean: ## 清理构建产物
	rm -f cshared/libztpl.so cshared/libztpl.dylib cshared/libztpl.h
	rm -f $(PYDIR)/ztpl/libztpl.* 
	rm -rf $(PYDIR)/dist $(PYDIR)/.pytest_cache
	find . -name __pycache__ -type d -exec rm -rf {} + 2>/dev/null || true

help: ## 显示可用目标
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
