# AIClean · 视觉 Skill：Paper Console（纸面控制台）

## 01 Identity
- 意象名：**Paper Console** —— 打印在高级纸上的精密控制台
- 概念种子：界面像一张排版精密的工程打印纸——墨迹清晰、线条分明、数字有力，所有"界面感"来自排版与线而不是装饰
- 一句话定位：WinDirStat/电脑管家式的密度，Linear/Notion 式的排版克制

## 02 Philosophy（五维）
| 维度 | 立场 |
|---|---|
| Space 空间 | 密排有节奏：8px 栅格，行高 40-44px 密表；分隔全部用 1px 发丝线，留白只用于区块大间距（≥24px），不用于装饰 |
| Color 色彩 | 准单色：暖纸灰底 + 深藏青墨色 + 唯一钴蓝交互色 + 陶橙主操作；状态色只做 10px 小圆点/小号文字，绝不做底色横幅 |
| Form 形式 | 小圆角 5-7px（控件 5px、卡片 7px、胶囊仅用于小标签）；1px 实线边框；静止态零阴影；悬浮层才允许一层柔影 |
| Motion 动态 | ≤150ms，只动 background/opacity/color；禁位移、缩放、弹跳、持续动画 |
| Material 材质 | 哑光纸面。禁玻璃拟态、噪点、渐变、水彩、光带、插画、3D |

## 03 适用边界
适用：Windows 桌面工具、数据密集列表、工程式管理界面。
不适用：营销页、情绪化品牌页、儿童/娱乐产品。

## 04 Design Tokens（强制字面使用）
```
/* 色 */
--paper:#F5F4F0        /* 全局底：暖纸灰，禁用纯白底页面 */
--card:#FFFFFF         /* 内容卡：纯白，靠 1px 边框浮起 */
--ink:#1D2B4B          /* 主墨色：深藏青 */
--ink-2:#5A6478        /* 次级文字 */
--ink-3:#939BAB        /* 弱化文字/占位 */
--line:#E6E3DA         /* 发丝线（卡片/表格） */
--line-2:#D8D4C8       /* 强一级线（表头上下框） */
--accent:#4169E1       /* 唯一交互蓝：链接、选中、进度、焦点 */
--accent-soft:#EDF1FD  /* 选中底色 */
--act:#FF8738          /* 主操作橙：仅主 CTA/关键数值 */
--ok:#2E9E5B --warn:#E0862A --bad:#D9534F   /* 状态色：只做小字/小点 */
/* 字 */
--sans:Segoe UI Variable Text,Segoe UI,Microsoft YaHei UI
--disp:Segoe UI Variable Display   /* 大数字 */
--mono:Cascadia Mono,Consolas      /* 路径/数值/编号 */
正文 13px/1.5 · 标题 14-16px/700 · 大数字 20-26px/750 字距-2% · 微字 11px
/* 形 */ --r-card:7px --r-ctl:5px --r-pill:999px(仅小标签) · 边框一律 1px
/* 距 */ 页边距 24px · 卡内 16-20px · 行高 40-44px · 栅格 8px
/* 动 */ 120-160ms ease · 仅 background/opacity/color
/* 影 */ 静态元素 0；浮层(--sh-pop):0 8px 24px rgba(29,43,75,.10)
```

## 05 Anti-Patterns（违规即返工）
| 禁止 | 违规例 | 正确 |
|---|---|---|
| 大圆角 | border-radius:14px+ | 卡 7px 控件 5px |
| 彩色横幅统计卡 | 整卡淡彩底 | 白卡 + 彩色小图标章/小字 |
| 渐变/发光 | linear-gradient、box-shadow 发光 | 平色 |
| 实心重选中 pill | 整块蓝底白字 | 浅蓝底+蓝字+2.5px 左标 |
| 装饰元素 | 水彩角/光带/插画/emoji | 删除 |
| 位移动效 | hover translate/scale | 只变色 |
| 双层以上阴影 | shadow-xl | 静态 0 影，浮层单层 |
| 彩条分组带 | 蓝/橙/紫分组横幅 | 统一中性灰底+深藏青字 |

## 06 工程约束
仅 CSS+内联 SVG 线性图标（stroke 1.5-1.7）；禁图片素材/Canvas/WebGL；动画仅 background/opacity/color；单文件 HTML（pack.go 打包），不改 Go 桥。

## 07 结构基调
- 侧栏：白底、细右线、logo 紧凑；选中=浅蓝底+蓝字+2.5px 左标
- 页头：小图标章+标题行+副标一行+右侧操作区，发丝线分隔
- 表格/清单：密行 40px、1px 行线、表头 11px 大写灰字上下双线
- 主操作区：吸底白卡细边或右上主按钮，橙仅一处
- 空态/错误态：居中 11px 灰字，无插画
