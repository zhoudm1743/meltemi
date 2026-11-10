# 文本输入、CSS 与 IME 扩展阶段设计

## 目标

在已完成的公开 Go UI API 基础上，连续交付文本输入、可配置样式和 Linux 输入法能力。用户明确要求将单行、多行、撤销/重做、CSS 和 IME 纳入同一个大阶段；实施时按依赖拆成可独立审查的子计划，不把它们混成一个不可验证的大任务。

## 交付顺序

1. **1B：单行 Input/Password** — 沿用 `2026-11-04-stage1b-input-password.md`。
2. **1C：多行文本与撤销/重做** — 在共享文本模型上增加多行和历史记录。
3. **1D：CSS 子集与组件样式** — 将已稳定组件接入统一外部样式。
4. **1E：Linux X11/XIM 基础 IME** — 在文本组件稳定后接入组合输入。

每个子计划必须独立通过单元测试、独立代码审查和真实场景验证后，才能进入下一个。

## 1B：单行输入基线

范围与原计划一致：rune 文本编辑、光标、选择、系统剪贴板、指针绑定、密码掩码、鼠标键盘交互和链式登录窗口。该基线是后续所有能力的依赖，不因扩展范围而省略。

## 1C：多行与撤销/重做

### 多行组件

- 新增公开 `TextArea(value *string) *TextAreaWidget`，不将 Password 扩展为多行。
- 支持 Enter 插入换行、UTF-8 文本、光标所在行列计算、上下左右移动、Home/End、鼠标点击与拖动选择、垂直滚动。
- 支持 Ctrl+A/C/V/X、Shift+方向/Home/End 选择，沿用系统剪贴板和绑定值语义。
- 文本绘制按行裁剪，长行允许水平滚动；首版不实现自动软换行。
- `SetValue` 同步绑定值、组件显示、光标和历史；直接修改绑定指针不自动通知。

### 撤销/重做

- 单行 Input 和多行 TextArea 共用历史模型，Password 也支持文本编辑历史。
- 保存文本、cursor 和 selection anchor；撤销/重做必须同时恢复这些内容。
- Ctrl+Z 撤销，Ctrl+Y 和 Ctrl+Shift+Z 重做。
- 连续普通输入合并为一个编辑单元；Backspace/Delete、选择替换、剪切、粘贴、SetValue 分别形成独立编辑单元。
- 新编辑清空 redo 分支；历史最多保留 100 个编辑单元，超出后丢弃最旧记录。
- 不提供公开历史对象 API；`Undo()`/`Redo()` 组件方法和键盘快捷键可用于测试。

## 1D：CSS 子集与组件样式

### 加载与解析

- 新增 `App.LoadStyles(path string) error`，应用级外部 CSS 文件在窗口创建前或运行中 UI 线程加载。
- 运行中加载后使所有窗口重新计算样式并重绘；不要求文件监听或热重载。
- 语法支持组件类型、`.class`、`#id`，以及 `:hover`、`:focus`、`:disabled`、`:checked`、`:pressed`。
- 首版选择器只支持一个类型/class/ID 加一个可选状态，不支持后代、逗号组合、属性选择器、伪元素、`!important`、媒体查询或动画。
- 支持颜色、背景、文字颜色、边框颜色/宽度、圆角、内外边距、宽高、字体和字号、Column gap。
- `px` 按逻辑单位解释，不使用物理像素。
- 解析错误返回文件、行列和属性信息；失败不覆盖已加载样式。

### 组件接入

- ColumnLayout、LabelWidget、ButtonWidget、InputWidget、TextAreaWidget 支持 `ID(string)` 和 `Class(string)` 链式元数据。
- 同一窗口内 ID 应唯一；重复 ID 在调试日志中报告，但不 panic。
- 样式优先级固定为：代码显式设置 > ID+状态 > ID > class+状态 > class > 类型+状态 > 类型 > 默认主题。
- 代码显式 Gap/Padding 等布局配置不被样式表覆盖。
- Password 的掩码、Input/TextArea 的选择、光标、边框和滚动绘制均读取同一解析后样式。
- 不支持系统菜单、托盘、文件对话框的 CSS。

## 1E：Linux X11/XIM 基础 IME

### 范围

- 仅 Linux X11，使用 cgo 与 Xlib/XIM；当前验证环境为 Fcitx 5。
- 支持组合文本开始、更新、提交、取消，组合状态显示在 Input/TextArea 内。
- 组合文本不得重复提交；普通 Rune 输入与 IME 提交必须区分。
- 根据当前光标窗口位置更新输入法候选位置，窗口移动和滚动时同步。
- 焦点切换、窗口隐藏/销毁和组件禁用必须取消组合并释放 XIM 资源。
- 当前不承诺 Wayland、Windows、macOS、所有候选列表皮肤或所有输入法框架。

### 技术约束

- GLFW 自身已创建 XIM input context 并通过 `Xutf8LookupString` 提交文本。实施前必须通过技术验证确定预编辑接入方式，避免第二个 input context 与 GLFW 重复消费同一提交。
- 首选在现有 X11 后端建立受控的 XIM 桥接，明确事件过滤、预编辑回调和 GLFW 提交事件的所有权。
- 如果证明共享 input context 不可行，允许在 X11 后端引入最小、可审计的 GLFW/XIM 适配补丁或替代事件桥，不修改 Fyne，不引入 WebView。
- XIM 技术验证必须先于生产集成；若预编辑无法可靠实现，不得把普通 Rune 输入冒充为 IME 完成，必须明确报告阻塞。

## 共同约束

- 不使用 Fyne、WebView、HTML 或 JavaScript，不新增大型依赖。
- 所有 UI 状态、文本历史、样式应用、XIM 回调和资源生命周期只在 UI 线程。
- 文本始终以 rune 索引，组合文本与已提交文本分开保存。
- 真实剪贴板测试保存并恢复旧值；桌面干扰型测试继续 opt-in。
- 非 Git 仓库，不自动初始化或提交；不使用 sudo。
- 每个子计划只引入其验收所需文件和测试，后续不得通过降低测试强度来赶进度。

## 验收

1. 单行和多行均能编辑中文、emoji、选择和剪贴板，历史恢复文本及选择。
2. 外部 CSS 能统一修改 Label/Button/Input/TextArea 外观，优先级符合设计，代码显式配置不被覆盖。
3. X11/Fcitx 下能显示预编辑、更新候选位置、提交中文且不重复，取消和销毁不残留组合或 XIM 资源。
4. 所有子计划全量测试、竞态检测、vet 和真实 smoke 通过；无法验证的平台或输入法明确披露。

## 实施补充裁决

以下是本设计为消除实现歧义所做的选择，不再留给实施者猜测：

1. **CSS 尺寸与布局**：`width`/`height` 只覆盖组件自然尺寸，属于布局前的首选值；Column 的 Go 布局仍决定是否水平拉伸，CSS 不得阻止拉伸。Input 默认 200x36 中的 36 可由 CSS height 覆盖。CSS padding 计入自然尺寸；不支持 margin。
2. **`:checked`**：本阶段只要求解析器和优先级支持，不新增 Checkbox 组件，也不要求现有组件产生 checked 状态。
3. **字体注册**：新增 `App.RegisterFont(name string, data []byte) error`，由应用提供完整 TTF/OTF 数据；内置仅 GoRegular。CSS `font-family` 只能引用已注册名称，未注册名称应用样式时报错并保留旧样式。首版不做逐 rune 回退或系统字体发现。中文真实验证使用 `/usr/share/fonts/opentype/source-han-cjk/SourceHanSansSC-Regular.otf`，由演示显式注册，避免 GoRegular 缺字被误认为中文显示成功。
4. **GLFW/XIM 依赖治理**：不 fork、不 vendor、不使用 `replace` 修改 `go-gl/glfw`。只允许项目内受控 XIM 桥接；若技术验证证明无法避免重复提交或无法获得预编辑，则停止 IME 集成并报告阻塞，不以其他实现冒充完成。
5. **IME 验证**：XIM 状态机和资源生命周期使用确定性单元测试；真实 Fcitx 5 使用真实 X11 会话中的合成按键、预编辑截图和状态检查，能自动化则纳入 opt-in smoke，否则使用明确的人工 checklist。真实验证必须覆盖预编辑显示、候选位置、提交不重复、取消、隐藏和销毁。
6. **覆盖与继承**：同优先级规则按样式表出现顺序，后者覆盖前者；仅 `font-family`、`font-size` 和文字颜色允许向子组件继承，其他属性不继承。
