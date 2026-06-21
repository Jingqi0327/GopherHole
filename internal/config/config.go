package config

import (
	"fmt"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// ClientConfig 客户端配置
type ClientConfig struct {
	Hostname     string `mapstructure:"HOSTNAME"`
	Server       string `mapstructure:"SERVER"`
	IP           string `mapstructure:"IP"`
	Token        string `mapstructure:"TOKEN"`
	ServerPubKey string `mapstructure:"SERVER_PUBLIC_KEY"`
}

// ServerConfig 服务端配置
type ServerConfig struct {
	BindAddr      string `mapstructure:"BIND_ADDR"`
	VirtualSubnet string `mapstructure:"VIRTUAL_SUBNET"`
	Token         string `mapstructure:"TOKEN"`
	PrivateKey    string `mapstructure:"PRIVATE_KEY"`
	PublicKey     string `mapstructure:"PUBLIC_KEY"`
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
	viper.SetDefault("TOKEN", "")
	viper.SetDefault("SERVER_PUBLIC_KEY", "")

	// 为了让命令行参数兼容大小写，我们手动绑定
	pflag.String("server", "", "Signaling Server address")
	pflag.String("ip", "", "Requested static Virtual IP (e.g. 10.8.0.5)")
	pflag.String("hostname", "", "Hostname (e.g. Workstation-PC)")
	pflag.String("token", "", "Authentication token")
	pflag.String("server_pub_key", "", "Server public key for TLS verification")
	pflag.Parse()

	_ = viper.BindPFlag("SERVER", pflag.CommandLine.Lookup("server"))
	_ = viper.BindPFlag("IP", pflag.CommandLine.Lookup("ip"))
	_ = viper.BindPFlag("HOSTNAME", pflag.CommandLine.Lookup("hostname"))
	_ = viper.BindPFlag("TOKEN", pflag.CommandLine.Lookup("token"))
	_ = viper.BindPFlag("SERVER_PUBLIC_KEY", pflag.CommandLine.Lookup("server_pub_key"))

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
	viper.SetDefault("TOKEN", "")
	viper.SetDefault("PRIVATE_KEY", "")
	viper.SetDefault("PUBLIC_KEY", "")

	pflag.String("bind_addr", "", "Server listen address")
	pflag.String("virtual_subnet", "", "Virtual subnet prefix (e.g. 10.8.0)")
	pflag.String("token", "", "Authentication token")
	pflag.String("private_key", "", "Server private key (base64 seed)")
	pflag.String("public_key", "", "Server public key (base64)")
	pflag.Parse()

	_ = viper.BindPFlag("BIND_ADDR", pflag.CommandLine.Lookup("bind_addr"))
	_ = viper.BindPFlag("VIRTUAL_SUBNET", pflag.CommandLine.Lookup("virtual_subnet"))
	_ = viper.BindPFlag("TOKEN", pflag.CommandLine.Lookup("token"))
	_ = viper.BindPFlag("PRIVATE_KEY", pflag.CommandLine.Lookup("private_key"))
	_ = viper.BindPFlag("PUBLIC_KEY", pflag.CommandLine.Lookup("public_key"))

	var cfg ServerConfig
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse server config: %w", err)
	}
	return &cfg, nil
}
