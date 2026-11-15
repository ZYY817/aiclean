//go:build ignore

// 打包前端源码为单个 web/index.html（嵌入 exe）：
//
//	go run pack.go
//
// 源码在 web/src/：index.html + css/*.css + js/*.js，按文件名顺序拼接。
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root, _ := filepath.Abs(filepath.Join("web", "src"))
	html, err := os.ReadFile(filepath.Join(root, "index.html"))
	must(err)
	css, js := "", ""
	files, err := filepath.Glob(filepath.Join(root, "css", "*.css"))
	must(err)
	for _, f := range files {
		b, err := os.ReadFile(f)
		must(err)
		css += fmt.Sprintf("/* ==== %s ==== */\n%s\n", filepath.Base(f), b)
	}
	files, err = filepath.Glob(filepath.Join(root, "js", "*.js"))
	must(err)
	for _, f := range files {
		b, err := os.ReadFile(f)
		must(err)
		js += fmt.Sprintf("/* ==== %s ==== */\n%s\n", filepath.Base(f), b)
	}
	out := strings.Replace(string(html), "<!-- @CSS@ -->", "<style>"+css+"</style>", 1)
	// 赞赏码图片直接编成 JS 常量打进前端（走桥接传 base64 不可靠）
	tips := ""
	for _, t := range [][2]string{{"wx", "tip-wx.jpg"}, {"zfb", "tip-zfb.jpg"}} {
		if b, err := os.ReadFile(filepath.Join("assets", t[1])); err == nil {
			tips += fmt.Sprintf("const TIP_%s = 'data:image/jpeg;base64,%s';\n", strings.ToUpper(t[0]), base64.StdEncoding.EncodeToString(b))
		}
	}
	out = strings.Replace(out, "<!-- @JS@ -->", "<script>"+tips+js+"</script>", 1)
	must(os.WriteFile(filepath.Join("web", "index.html"), []byte(out), 0644))
	fmt.Printf("packed %d bytes -> web/index.html\n", len(out))
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
