# 发布到 PyPI

两个包：

| 包 | 内容 | wheel 类型 |
|---|---|---|
| `zdog-template` | Python SDK + 平台相关的 `libztpl.so` | **每平台一个** |
| `zdog-template-cli` | 命令行，纯 Python，依赖 `zdog-template` | 一个通吃 |

## 一次性准备：Trusted Publishing

用 OIDC 发布，**不需要在仓库里存 API token**。

包名首次发布前 PyPI 上还没有项目，所以要用 **pending publisher**：
<https://pypi.org/manage/account/publishing/> → 添加，两个包各填一次：

| 字段 | SDK | CLI |
|---|---|---|
| PyPI Project Name | `zdog-template` | `zdog-template-cli` |
| Owner | `litterzhang` | `litterzhang` |
| Repository name | `zdog-template` | `zdog-template` |
| Workflow name | `release.yml` | `release.yml` |
| Environment name | `release-sdk` | `release-cli` |

**两个包必须用不同的 environment。** PyPI 的可信发布者是按
`(owner, repo, workflow, environment)` 匹配的 —— 共用一个环境意味着任何一个
发布 job 拿到的 OIDC 令牌对两个项目都有效，CLI 的构建被污染就能推 SDK。
分开之后每个 job 只能发自己那个包，也便于分别设审批。

GitHub 侧这两个 environment 已由 `make gh-setup` 建好。
建议各加一条人工审批（Settings → Environments → 选中 → Required reviewers），
这样每次发布都要点一下确认。

## 发布

版本号在 `sdk/python/pyproject.toml` 和 `cli/pyproject.toml` 里，两个包锁步。
改完、更新 CHANGELOG（**必须有对应版本的一节**，否则 `github-release` job 会
直接失败）、提交，然后：

```bash
git tag v0.1.1 && git push origin v0.1.1
```

workflow 会构建各平台 wheel、在干净环境里冒烟验证，然后发到 PyPI。
也可以 `gh workflow run release.yml` 手动触发（用于补发某个平台）。

验证：

```bash
uv run --with zdog-template python -c "
from ztemplate import Template
print(Template('[\${a}] \${b}', target='\${b}/\${a}').transform_text('[x] y'))"
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
  make build && cp cshared/libztpl.so sdk/python/ztemplate/
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

`cli/pyproject.toml` 里有 `[tool.uv.sources] zdog-template = { path = "../sdk/python" }`，
方便本地开发。这段是 uv 专有的、不会进 wheel 元数据，但 release workflow 仍会
在构建前把它剥掉，免得哪天 uv 改了行为。

## 覆盖的平台

当前只构建：

- `manylinux_2_28_x86_64`
- `macosx_26_0_arm64` —— **注意这个下限是错的**

### macOS 的下限被构建机版本污染了

matrix 里 macOS 那行把标签留空、交给 `sysconfig.get_platform()` 推断，而它报的是
**runner 的系统版本**（现在是 Tahoe），不是我们支持的最低版本。结果是只有
macOS 26+ 装得上，15/14/13 的用户会看到"找不到匹配的发行版"。

这跟 Linux 那边是同一类问题 —— 构建机的版本泄漏成兼容性下限 —— 只不过 Linux
处理对了（manylinux 老容器 + 显式标签），macOS 漏了。

修法是显式指定标签、并给 Go 构建设部署目标：

```yaml
- name: macos-arm64
  os: macos-latest
  tag: py3-none-macosx_11_0_arm64
```

```bash
MACOSX_DEPLOYMENT_TARGET=11.0 go build -buildmode=c-shared ...
```

Go 的 darwin/arm64 本来就以 macOS 11 为最低支持，所以标 `11_0` 是诚实的，
不是把断言放宽到没有依据的程度。

其它平台（Windows、Linux ARM、macOS x86_64）需要往 `release.yml` 的 matrix
里加。没有对应 wheel 的用户可以自己 `make build` 再用 `ZTPL_LIB` 指向它。
