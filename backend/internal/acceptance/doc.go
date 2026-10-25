// Package acceptance 只包含黑盒验收与压测测试：通过 app.Run 启动真实服务，
// 经 HTTP API / SSE 与本机 loopback 模拟器交互，不访问真实设备、渠道或主机。
package acceptance
