package browser

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// stealthManifest 是一个临时 WebExtension 的清单。它以 document_start 注入
// 内容脚本，在 Cloudflare 等页面脚本运行之前隐藏 Marionette 暴露的
// navigator.webdriver 标记，避免验证页反复提示“重试”。
const stealthManifest = `{
  "manifest_version": 2,
  "name": "ugal browser helper",
  "version": "1.0",
  "permissions": ["<all_urls>"],
  "content_scripts": [{
    "matches": ["<all_urls>"],
    "js": ["stealth.js"],
    "run_at": "document_start",
    "all_frames": true
  }]
}
`

const stealthScript = `(function () {
  try {
    var w = window.wrappedJSObject;
    Object.defineProperty(w.Navigator.prototype, "webdriver", {
      get: exportFunction(function () { return false; }, window),
      configurable: true
    });
  } catch (e) {}
})();
`

// installStealthAddon 把内置的临时扩展安装到当前 Firefox 会话，用在导航到
// 验证页之前。扩展只存在于本次临时配置中，浏览器退出后自动消失。
func installStealthAddon(ctx context.Context, client *marionetteClient, profile string) error {
	xpiPath := filepath.Join(profile, "ugal-stealth.xpi")
	if err := buildStealthXPI(xpiPath); err != nil {
		return err
	}
	_, err := client.call(ctx, "Addon:Install", map[string]interface{}{
		"path":      xpiPath,
		"temporary": true,
	}, 30*time.Second)
	if err != nil {
		return err
	}
	return nil
}

func buildStealthXPI(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建浏览器辅助扩展失败: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range map[string]string{
		"manifest.json": stealthManifest,
		"stealth.js":    stealthScript,
	} {
		w, err := zw.Create(name)
		if err != nil {
			zw.Close()
			return fmt.Errorf("写入浏览器辅助扩展失败: %w", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			zw.Close()
			return fmt.Errorf("写入浏览器辅助扩展失败: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("写入浏览器辅助扩展失败: %w", err)
	}
	return nil
}
