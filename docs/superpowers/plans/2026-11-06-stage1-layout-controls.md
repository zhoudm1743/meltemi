# Stage 1 Layout and Basic Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 `Row`、`Stack`、`Scroll`、`Spacer`、`Checkbox`、`Image` 和 CSS `opacity`，使布局与基础组件达到总体设计的阶段 1 范围。

**Architecture:** 沿用封闭的 `Component` 与内部 measure/arrange/draw/event 契约，以容器负责裁剪和弹性空白分配。Scroll 和 Stack 使用递归、可裁剪的命中测试；Image 通过内部 painter 扩展进入 OpenGL，不向应用暴露 GL 类型。所有新组件接入已有样式遍历。

**Tech Stack:** Go 1.26、标准库 `image`、既有 GLFW/OpenGL 3.3、内部 style/text/render；不新增第三方依赖。

**Spec:** `doc/总体设计.md` 第 3、5.3、5.4、7、10、11、13、14、15 节。

## Global Constraints

- 不使用 Fyne、WebView、HTML 或 JavaScript，不新增依赖。
- 公共 API 不暴露 GLFW、OpenGL、内部 driver/render 类型；`image.Image` 是允许的标准库输入。
- 所有 UI 状态和回调只在 UI 线程；工作 goroutine 只能通过 `App.Post` 更新。
- `Constraints.Max` 零值维度表示不限；空容器、零尺寸和负偏移不得产生越界绘制或 panic。
- 保持现有 Column、Input、TextArea、IME、CSS 优先级和资源生命周期兼容。
- 当前不是 Git 仓库，不初始化、不提交、不使用 sudo。

## Review Focus

- Stack 的重叠兄弟：只能命中最上层可见组件；由 Task 1 的 `TestStackHitTestTopmost` 固定。
- Scroll 裁剪外的内容：不可见、不可点击，滚轮只消费实际发生的滚动；由 Task 2 的 `TestScrollClipsAndHitTestsViewport` 和 `TestScrollWheelAtLimits` 固定。
- Spacer 与固定子组件混排：多余主轴空间按 Spacer 数量分配，不足时不产生负尺寸；由 Task 1 的 `TestRowAndColumnDistributeSpacers` 固定。
- Checkbox 禁用或外部释放：不得改变绑定值或调用回调；由 Task 3 的 `TestCheckboxDisabledAndReleaseOutside` 固定。
- 图片解码失败、空图片、GL 纹理释放失败路径：返回明确错误且不泄漏窗口资源；由 Task 4 的 `TestLoadImageRejectsInvalidData` 和 `TestImageRendererLifecycle` 固定。
- 父子 opacity：按乘法合成，0 完全透明但仍保持布局，样式解析拒绝越界和非数值；由 Task 5 的 `TestOpacityParsingAndNestedDrawing` 固定。

---

### Task 1: Row、Stack、Spacer 与容器遍历

**Files:**
- Create: `ui/row.go`, `ui/stack.go`, `ui/spacer.go`
- Modify: `ui/column.go`, `ui/layout.go`, `ui/event.go`, `ui/app.go`, `ui/style.go`, `internal/style/parse.go`, `internal/style/parse_test.go`
- Test: `ui/row_test.go`, `ui/stack_test.go`, `ui/spacer_test.go`, `ui/event_test.go`

**Interfaces:**
- Produces: `Row(...Component) *RowLayout`、`Stack(...Component) *StackLayout`、`Spacer() *SpacerWidget`；Row 提供 `ID/Class/Gap/Padding`，Stack 提供 `ID/Class/Padding`。
- Spacer 无自然尺寸；Row/Column 将主轴剩余空间平均分给直接子 Spacer，交叉轴拉伸。Stack 对所有子组件使用同一内容矩形并按声明顺序绘制，后声明者在最上层。
- 样式解析器预先接受 `Row/Stack/Scroll/Spacer/Checkbox/Image` 类型名，后续任务分别实现对应组件；本任务只需 Row/Stack/Spacer 的实际样式元数据。

- [ ] **Step 1: 写失败测试**：实现 `TestRowMeasureArrangeAndStyles`、`TestStackMeasureArrangeAndStyles`、`TestRowAndColumnDistributeSpacers`、`TestStackHitTestTopmost`，覆盖空容器、单子组件、padding 大于可用空间和 Spacer 空间不足。
- [ ] **Step 2: 运行 `go test ./ui -run 'Test(Row|Stack|Spacer|Column)' -v`，确认因新 API/行为缺失而失败。**
- [ ] **Step 3: 实现三种组件并扩展 Column 的 Spacer 分配。** 不改公共 Component 的封闭性；为容器增加内部 children 遍历接口，替换 `styleTransaction.prepareComponent` 和样式绘制中的 Column 专用类型断言。
- [ ] **Step 4: 将命中测试改为递归容器遍历。** Stack 逆序检查子组件；普通容器保持声明顺序且每次事件至多投递一次，不依赖“兄弟不重叠”的 Column 假设。
- [ ] **Step 5: 运行 `go test ./ui` 和 `go test ./internal/style`，确认全部通过。**

### Task 2: Scroll 视口与滚轮事件

**Files:**
- Create: `ui/scroll.go`, `ui/scroll_test.go`
- Modify: `internal/driver/types.go`, `internal/driver/glfw/events.go`, `internal/driver/glfw/events_test.go`, `ui/event.go`, `ui/app.go`, `ui/style.go`

**Interfaces:**
- Produces: `Scroll(content Component) *ScrollLayout`，链式 `ID/Class/Padding`，`SetOffset(x, y float64)`、`Offset() (x, y float64)`。
- 内部新增 `driver.EventScroll`，事件 `ScrollX/ScrollY float64`；GLFW scroll callback 转为逻辑滚动增量，组件不接收 GLFW 事件。
- Scroll 视口尺寸由约束决定，内容在反方向保持自然尺寸；offset 钳制到 `[0, contentSize-viewportSize]`，默认 wheel 一行/一格使用固定逻辑步长 40。

- [ ] **Step 1: 写失败测试**：`TestScrollMeasuresContentAndClampsOffset`、`TestScrollClipsAndHitTestsViewport`、`TestScrollWheelAtLimits`、`TestGLFWScrollEventMapping`，覆盖垂直、水平、负 delta、缩小后的 offset 重钳制。
- [ ] **Step 2: 运行 `go test ./ui ./internal/driver/glfw -run 'Test(Scroll|GLFWScroll)' -v`，确认失败。**
- [ ] **Step 3: 实现 Scroll 的测量、排列、嵌套裁剪、递归命中测试与 wheel 消费。** 保证 clip 在 defer 中恢复，子组件超出视口不命中；将新容器接入样式遍历和 componentBounds。
- [ ] **Step 4: 实现并清理 GLFW scroll callback，保持 IME 和现有 mouse/key callback 行为不变。**
- [ ] **Step 5: 运行 `go test ./ui ./internal/driver/...`，确认全部通过。**

### Task 3: Checkbox 组件

**Files:**
- Create: `ui/checkbox.go`, `ui/checkbox_test.go`
- Modify: `ui/style.go`, `ui/column.go`, `ui/theme.go`, `internal/style/parse.go`, `internal/style/parse_test.go`

**Interfaces:**
- Produces: `Checkbox(text string, value *bool) *CheckboxWidget`，`Value() bool`、`SetValue(bool)`、`SetEnabled(bool)`、`OnChange(func(bool))`、`ID/Class`。
- SetValue 同步非 nil 绑定指针和显示；OnChange 只在实际值改变时调用。鼠标点击和聚焦 Enter/Space 切换；disabled 不响应。样式元数据包含 hover/focus/disabled/checked/pressed。

- [ ] **Step 1: 写失败测试**：`TestCheckboxBindingAndCallbacks`、`TestCheckboxKeyboardAndPointer`、`TestCheckboxDisabledAndReleaseOutside`、`TestCheckboxCheckedStyle`，覆盖 nil 绑定、重复 SetValue 不回调、焦点丢失。
- [ ] **Step 2: 运行 `go test ./ui ./internal/style -run 'TestCheckbox' -v`，确认失败。**
- [ ] **Step 3: 实现 Checkbox 的文本、勾选标记、状态与事件契约，并加入组件样式绘制/遍历。** 为 Space 增加后端无关的内部按键映射，不引入公共按键类型。
- [ ] **Step 4: 运行 `go test ./ui ./internal/style ./internal/driver/glfw`，确认全部通过。**

### Task 4: Image 组件与 OpenGL 图片绘制

**Files:**
- Create: `ui/image.go`, `ui/image_test.go`, `internal/render/gl/image.go`, `internal/render/gl/image_test.go`
- Modify: `ui/component.go`, `ui/style.go`, `ui/column.go`, `internal/render/render.go`, `internal/render/gl/renderer.go`

**Interfaces:**
- Produces: `Image(source image.Image) *ImageWidget`、`LoadImage(path string) (*ImageWidget, error)`、`SetImage(image.Image)`、`ID/Class`。
- 内部 `render.Image` 持有不可变 RGBA 像素和尺寸，UI 在 SetImage 时从标准库图片转换。 painter 增加 `DrawImage(source *render.Image, destination render.Rect, opacity float64) error`；GL renderer 按 `*render.Image` 指针缓存纹理并在 Close 释放，Discard 仅释放 Go 引用。opacity 是 drawContext 已合成的最终 alpha，renderer 不重复应用组件透明度。
- 默认自然尺寸为图片像素尺寸；CSS width/height 可覆盖并拉伸到目标矩形。空/nil 图片尺寸为 0 且不调用 GL。

- [ ] **Step 1: 写失败测试**：`TestImageNaturalAndStyledSize`、`TestLoadImagePNGAndInvalidData`、`TestImageDrawDestination`、`TestImageRendererLifecycle`，使用标准库生成 PNG，不提交二进制 fixture。
- [ ] **Step 2: 运行 `go test ./ui ./internal/render/gl -run 'Test(Image|LoadImage)' -v`，确认失败。**
- [ ] **Step 3: 实现图片组件和内部 painter 扩展，保持既有 fake painter 编译通过；LoadImage 支持标准库 PNG/JPEG/GIF 并返回带路径的错误。**
- [ ] **Step 4: 实现 GL RGBA 上传、纹理复用、framebuffer/logical 缩放、裁剪和纹理释放。** 不允许跨 GL context 共享纹理句柄。
- [ ] **Step 5: 运行 `go test ./ui ./internal/render/gl`，确认全部通过。**

### Task 5: CSS opacity 与综合演示

**Files:**
- Create: `cmd/wind-layout/main.go`
- Modify: `internal/style/parse.go`, `internal/style/value.go`, `internal/style/resolve.go`, `ui/style.go`, `ui/component.go`, `ui/column.go`, `ui/draw_test.go`
- Test: `internal/style/parse_test.go`, `ui/style_test.go`, `cmd/wind-layout/main_test.go`

**Interfaces:**
- CSS 新增 `opacity: <number>`，范围 `[0,1]`；父子绘制 alpha 相乘，默认 1。
- 演示组合 Row/Column/Stack/Scroll/Spacer/Checkbox/Image，并展示 disabled opacity；不引入新运行时服务。

- [ ] **Step 1: 写失败测试**：`TestOpacityParsingAndNestedDrawing`、`TestOpacityRejectsInvalidValues`、`TestEveryComponentAcceptsOpacity`，断言 rect/text/image alpha 与嵌套乘法。
- [ ] **Step 2: 运行 `go test ./ui ./internal/style -run 'TestOpacity|TestEveryComponent' -v`，确认失败。**
- [ ] **Step 3: 实现 opacity 解析、样式继承边界和 drawContext 合成。** opacity 不继承为普通 CSS 属性，但父组件绘制子树时形成乘法上下文；恢复上下文即使绘制出错也必须执行。
- [ ] **Step 4: 添加真实 GLFW 演示和确定性演示配置测试，更新受影响注释。**
- [ ] **Step 5: 运行 `go test ./...`、`go vet ./...` 和 `gofmt -l` 检查本任务修改文件；真实桌面 smoke 记录命令和结果，环境失败不得冒充通过。**
