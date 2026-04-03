  # tmux-ghostty macOS 发布与安装方案

  ## Summary

  - 这个仓库当前会编译出 2 个二进制：
      - tmux-ghostty
      - tmux-ghostty-broker
  - 按你的要求，安装包里同时包含这两个二进制。
  - 安装目标保持为 macOS 原生体验：
      - 主分发物：签名并公证的 .pkg
      - 发布渠道：GitHub Release
      - 更新方式：GitHub Release 手动升级 + CLI 自更新
      - 卸载方式：CLI 内建卸载命令，并且会同时删除两个二进制

  ## Key Changes

  ### 1. 安装布局

  - .pkg 安装内容固定为：
      - /usr/local/bin/tmux-ghostty
      - /usr/local/bin/tmux-ghostty-broker
  - 不再依赖 serve-broker fallback 作为用户分发主路径；安装后的正式运行路径优先是主程序调用同机已安装的 tmux-ghostty-broker。
  - 仍保留现有 fallback 逻辑作为兼容兜底，但发布方案以“双二进制安装”作为标准行为。

  ### 2. CLI 增强

  - 新增命令：
      - tmux-ghostty version
      - tmux-ghostty self-update
      - tmux-ghostty self-update --check
      - tmux-ghostty self-update --version <tag>
      - tmux-ghostty uninstall
  - tmux-ghostty uninstall 行为固定为：
      1. 检查并停止 broker 进程
      2. 删除 /usr/local/bin/tmux-ghostty
      3. 删除 /usr/local/bin/tmux-ghostty-broker
      4. 清理当前执行用户的 ~/Library/Application Support/tmux-ghostty
      5. 清理 pkg receipt
  - uninstall 需要管理员权限时，明确提示用户使用 sudo tmux-ghostty uninstall。

  ### 3. 更新机制

  - self-update 依托 GitHub Release。
  - Release 资产固定包含：
      - tmux-ghostty_<version>_darwin_universal.pkg
      - tmux-ghostty_<version>_darwin_universal.tar.gz
      - checksums.txt
  - self-update 的标准流程固定为：
      1. 读取当前版本
      2. 查询 GitHub 最新 release 或指定 tag
      3. 下载 .pkg
      4. 校验 checksum
      5. 调用系统安装器覆盖安装
  - 因为安装的是系统目录里的两个二进制，self-update 默认允许要求 sudo。

  ### 4. 发布自动化

  - 新增 GitHub Actions release workflow，触发条件为 v* tag。
  - workflow 固定执行：
      1. 构建 darwin/amd64 与 darwin/arm64
      2. 分别产出两个程序的架构二进制
      3. 合并 universal 版 tmux-ghostty
      4. 合并 universal 版 tmux-ghostty-broker
      5. 打包 .tar.gz
      6. 生成 .pkg
      7. 签名、公证、staple
      8. 发布到 GitHub Release
  - .pkg 的 payload 必须包含两个二进制，不能只装主 CLI。

  ### 5. 文档与发布说明

  - README 增加以下内容：
      - 当前会产出 2 个二进制
      - .pkg 安装后会把两个命令都放到 /usr/local/bin
      - tmux-ghostty uninstall 会同时删除两个二进制
      - 如何通过 GitHub Release 安装和升级
      - 如何打 tag 触发自动发布

  ## Public Interfaces

  - 新增用户可见命令：
      - tmux-ghostty version
      - tmux-ghostty self-update
      - tmux-ghostty self-update --check
      - tmux-ghostty self-update --version <tag>
      - tmux-ghostty uninstall
  - 安装后的用户可见二进制变为两个：
      - tmux-ghostty
      - tmux-ghostty-broker
  - 不修改现有 broker RPC 接口。

  ## Test Plan

  - 构建测试：
      - go build ./cmd/tmux-ghostty
      - go build ./cmd/tmux-ghostty-broker
      - 两个 universal binary 都能执行
  - 安装测试：
      - 安装 .pkg 后，which tmux-ghostty 和 which tmux-ghostty-broker 都可解析
      - tmux-ghostty up 能正常拉起 broker
      - 主程序优先找到已安装的 tmux-ghostty-broker
  - 更新测试：
      - tmux-ghostty self-update --check 能发现新版本
      - tmux-ghostty self-update --version <tag> 成功覆盖两个二进制
  - 卸载测试：
      - sudo tmux-ghostty uninstall 能停止 broker
      - 两个二进制都被删除
      - 运行时目录被清理
      - 卸载后两个命令都不可执行
  - 发布测试：
      - 推送 tag 后自动生成 GitHub Release
      - Release 附件包含 .pkg、.tar.gz、checksums.txt
      - .pkg 安装结果与本地预期一致

  ## Assumptions

  - 标准安装目录固定为 /usr/local/bin。
  - 首版只支持 系统级安装，不做用户目录安装模式。
  - tmux-ghostty-broker 虽然是辅助进程，但仍作为显式二进制随安装包一起安装，并允许用户直接调用。
  - 卸载命令默认只清理当前执行用户的 ~/Library/Application Support/tmux-ghostty。
  - 标准发布流程仍然是：
      1. 先把安装/发布代码 push 到 GitHub
      2. 创建 vX.Y.Z tag
      3. push tag 触发 GitHub Actions 自动发布
