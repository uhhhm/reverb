package main

import "os"

const webkitDMABufEnv = "WEBKIT_DISABLE_DMABUF_RENDERER"

// configureWebView adjusts the environment WebKitGTK reads when the window's
// webview starts, so it has to run before the window is created.
func configureWebView() { disableDMABufOnNVIDIA("/sys/module/nvidia") }

// disableDMABufOnNVIDIA turns off WebKitGTK's DMA-BUF renderer when the NVIDIA
// kernel driver (nvidiaModule) is loaded. On that driver the renderer fails to
// allocate its GBM buffers and the window stays blank white; the fallback
// renderer draws normally. A value the user set is left alone.
func disableDMABufOnNVIDIA(nvidiaModule string) {
	if _, set := os.LookupEnv(webkitDMABufEnv); set {
		return
	}
	if _, err := os.Stat(nvidiaModule); err == nil {
		_ = os.Setenv(webkitDMABufEnv, "1")
	}
}
