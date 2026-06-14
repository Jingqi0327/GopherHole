package config

import (
	"fmt"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// ClientConfig 客户端配置
type ClientConfig struct {
	Hostname string `mapstructure:"HOSTNAME"`
	Server   string `mapstructure:"SERVER"`
	IP       string `mapstructure:"IP"`
}

// ServerConfig 服务端配置
type ServerConfig struct {
	BindAddr      string `mapstructure:"BIND_ADDR"`
	VirtualSubnet string `mapstructure:"VIRTUAL_SUBNET"`
}

// initViper 初始化 viper 的公共逻辑
func initViper() {
	viper.SetConfigFile(".env")
	// 忽略找不到 .env 文件的错误，允许纯命令行或系统环境变量启动
	_ = viper.ReadInConfig()

	// 自动读取环境变量 (默认会将键名转为大写来匹配环境变量)
	viper.AutomaticEnv()
}

// LoadClientConfig 加载客户端配置
func LoadClientConfig() (*ClientConfig, error) {
	initViper()

	viper.SetDefault("SERVER", "127.0.0.1:8086")
	viper.SetDefault("IP", "")
	viper.SetDefault("HOSTNAME", "")

	// 为了让命令行参数兼容大小写，我们手动绑定
	pflag.String("server", "", "Signaling Server address")
	pflag.String("ip", "", "Requested static Virtual IP (e.g. 10.8.0.5)")
	pflag.String("hostname", "", "Hostname (e.g. Workstation-PC)")
	pflag.Parse()

	_ = viper.BindPFlag("SERVER", pflag.CommandLine.Lookup("server"))
	_ = viper.BindPFlag("IP", pflag.CommandLine.Lookup("ip"))
	_ = viper.BindPFlag("HOSTNAME", pflag.CommandLine.Lookup("hostname"))

	var cfg ClientConfig
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse client config: %w", err)
	}
	return &cfg, nil
}

// LoadServerConfig 加载服务端配置
func LoadServerConfig() (*ServerConfig, error) {
	initViper()

	viper.SetDefault("BIND_ADDR", ":8086")
	viper.SetDefault("VIRTUAL_SUBNET", "10.8.0")

	pflag.String("bind_addr", "", "Server listen address")
	pflag.String("virtual_subnet", "", "Virtual subnet prefix (e.g. 10.8.0)")
	pflag.Parse()

	_ = viper.BindPFlag("BIND_ADDR", pflag.CommandLine.Lookup("bind_addr"))
	_ = viper.BindPFlag("VIRTUAL_SUBNET", pflag.CommandLine.Lookup("virtual_subnet"))

	var cfg ServerConfig
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse server config: %w", err)
	}
	return &cfg, nil
}
