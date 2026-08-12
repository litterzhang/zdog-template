# 发布到 PyPI

两个包：

| 包 | 内容 | wheel 类型 |
|---|---|---|
| `ztpl` | Python SDK + 平台相关的 `libztpl.so` | **每平台一个** |
| `ztpl-cli` | 命令行，纯 Python，依赖 `ztpl` | 一个通吃 |

## 一次性准备：Trusted Publishing

用 OIDC 发布，**不需要在仓库里存 API token**。到 PyPI 项目设置 →
Publishing → 添加可信发布者：

| 字段 | 值 |
|---|---|
| Owner | `litterzhang` |
| Repository | `zdog-template` |
| Workflow | `release.yml` |
| Environment | `release` |

TestPyPI 同样操作一遍。然后在 GitHub 仓库设置里建一个名为 `release` 的
environment（可以顺便加上人工审批）。

## 发布

```bash
# 先发 TestPyPI 试水
gh workflow run release.yml -f target=testpypi

# 验证：装下来跑一遍
uv run --with ztpl --index https://test.pypi.org/simple/ \
  python -c "from ztpl import Template; print(Template('[\${a}]', target='\${a}').transform_text('[x]'))"

# 没问题再打 tag 正式发布
git tag v0.1.0 && git push origin v0.1.0
```

## 几个必须知道的坑

### wheel 标签

包里带 `.so`，就**不是**纯 Python 包。默认标签 `py3-none-any` 会让 macOS 用户
装到 Linux 的库、import 就炸。

标签由 `sdk/python/hatch_build.py` 设置 —— hatchling 的这个开关**只能由构建
钩子控制**，静态配置里写 `pure-python = false` 是无效的（`WheelBuilderConfig`
上并没有那个选项）。

我们要的是 `py3-none-<平台>`，不是 hatchling 默认推断的 `cp313-cp313-<平台>`：
库是用 `ctypes` 加载的，不碰 CPython 的 C-API，所以**任何 Python 3 都能用**，
不该绑死解释器版本。

### Linux 必须用 manylinux

**PyPI 直接拒绝 `linux_x86_64` 标签的 wheel。** 而且直接在 ubuntu runner 上
构建出的 `.so` 会要求 GLIBC 2.34（2021 年的版本），很多生产环境没有。

所以 release workflow 在 `quay.io/pypa/manylinux_2_28_x86_64` 容器里构建，
并用 `ZTPL_WHEEL_TAG` 指定标签。

```bash
# 本地想复现的话
docker run --rm -v "$PWD:/src" -w /src quay.io/pypa/manylinux_2_28_x86_64 bash -c '
  curl -fsSL https://go.dev/dl/go1.25.0.linux-amd64.tar.gz | tar -C /usr/local -xz
  export PATH=$PATH:/usr/local/go/bin
  make build && cp cshared/libztpl.so sdk/python/ztpl/
  cd sdk/python && ZTPL_WHEEL_TAG=py3-none-manylinux_2_28_x86_64 uv build --wheel'
```

### 必须 `uv build --wheel`

默认的 `uv build` 会**先打 sdist、再从 sdist 构建 wheel**，而 `.so` 是平台产物、
不该进 sdist —— 于是 wheel 里就没有库了。这个问题在写 CI 当天就撞到过一次，
只有"装到干净环境里跑一遍"才发现得了，所以 workflow 里保留了那步冒烟测试。

### 不发 sdist

sdist 里没有 `.so`，装的人需要有 Go 工具链才能用 —— 但 `pip install` 不会
帮你跑 `go build`。与其发一个装上就报错的包，不如只发平台 wheel。
用户没有对应平台的 wheel 时，`pip` 会明确报"找不到匹配的发行版"，
比装上之后运行时才发现缺库要好。

### CLI 的路径依赖

`cli/pyproject.toml` 里有 `[tool.uv.sources] ztpl = { path = "../sdk/python" }`，
方便本地开发。这段是 uv 专有的、不会进 wheel 元数据，但 release workflow 仍会
在构建前把它剥掉，免得哪天 uv 改了行为。

## 覆盖的平台

当前只构建：

- `manylinux_2_28_x86_64`
- `macosx_*_arm64`

其它平台（Windows、Linux ARM、macOS x86_64）需要往 `release.yml` 的 matrix
里加。没有对应 wheel 的用户可以自己 `make build` 再用 `ZTPL_LIB` 指向它。
