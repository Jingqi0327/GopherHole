package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TokenAuth 实现 grpc.PerRPCCredentials 接口，用于在每次 RPC 调用时携带 Token。
type TokenAuth struct {
	token string
}

// NewTokenAuth 创建一个新的 TokenAuth 实例
func NewTokenAuth(token string) *TokenAuth {
	return &TokenAuth{token: token}
}

// GetRequestMetadata 获取请求元数据，将 token 附加到头部
func (t *TokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

// RequireTransportSecurity 指示是否需要传输层安全（TLS）
func (t *TokenAuth) RequireTransportSecurity() bool {
	// 因为我们自己实现了 TLS 极客认证，这里返回 true 保证底层是加密的
	return true
}

// authenticate 核心鉴权逻辑，用于提取并验证 token
func authenticate(ctx context.Context, validToken string) error {
	// 如果服务端没有配置 token，则默认允许所有连接
	if validToken == "" {
		return nil
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Errorf(codes.Unauthenticated, "missing metadata")
	}

	authHeader, ok := md["authorization"]
	if !ok || len(authHeader) == 0 {
		return status.Errorf(codes.Unauthenticated, "missing authorization token")
	}

	// 格式应为 "Bearer <token>"
	tokenStr := authHeader[0]
	if !strings.HasPrefix(tokenStr, "Bearer ") {
		return status.Errorf(codes.Unauthenticated, "invalid authorization format")
	}

	token := strings.TrimPrefix(tokenStr, "Bearer ")
	if token != validToken {
		return status.Errorf(codes.Unauthenticated, "invalid token")
	}

	return nil
}

// NewAuthUnaryInterceptor 创建用于 Unary RPC 的服务端鉴权拦截器
func NewAuthUnaryInterceptor(validToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := authenticate(ctx, validToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// NewAuthStreamInterceptor 创建用于 Stream RPC 的服务端鉴权拦截器
func NewAuthStreamInterceptor(validToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authenticate(ss.Context(), validToken); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
