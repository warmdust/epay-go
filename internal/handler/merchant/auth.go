// internal/handler/merchant/auth.go
package merchant

import (
	"os"

	"github.com/example/epay-go/internal/service"
	"github.com/example/epay-go/pkg/jwt"
	"github.com/example/epay-go/pkg/response"
	"github.com/gin-gonic/gin"
)

// registerCode 商户注册口令，可通过环境变量 REGISTER_CODE 覆盖
func registerCode() string {
	if code := os.Getenv("REGISTER_CODE"); code != "" {
		return code
	}
	return "gzdzl123"
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=4,max=32"`
	Password string `json:"password" binding:"required,min=6,max=32"`
	Email    string `json:"email" binding:"omitempty,email"`
	Phone    string `json:"phone" binding:"omitempty"`
	RegCode  string `json:"reg_code" binding:"required"`
}

// Register 商户注册
func Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ParamError(c, "参数错误: "+err.Error())
		return
	}

	if req.RegCode != registerCode() {
		response.ParamError(c, "注册密码错误")
		return
	}

	merchantService := service.NewMerchantService()
	merchant, err := merchantService.Register(&service.RegisterRequest{
		Username: req.Username,
		Password: req.Password,
		Email:    req.Email,
		Phone:    req.Phone,
	})
	if err != nil {
		response.Error(c, response.CodeParamError, err.Error())
		return
	}

	// 生成 Token
	token, err := jwt.GenerateToken(merchant.ID, merchant.Username, jwt.TokenTypeMerchant)
	if err != nil {
		response.ServerError(c, "生成Token失败")
		return
	}

	response.Success(c, gin.H{
		"token": token,
		"merchant": gin.H{
			"id":       merchant.ID,
			"username": merchant.Username,
			"api_key":  merchant.ApiKey,
		},
	})
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login 商户登录
func Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ParamError(c, "参数错误")
		return
	}

	merchantService := service.NewMerchantService()
	merchant, err := merchantService.Login(&service.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		response.Error(c, response.CodeUnauthorized, err.Error())
		return
	}

	// 生成 Token
	token, err := jwt.GenerateToken(merchant.ID, merchant.Username, jwt.TokenTypeMerchant)
	if err != nil {
		response.ServerError(c, "生成Token失败")
		return
	}

	response.Success(c, gin.H{
		"token": token,
		"merchant": gin.H{
			"id":       merchant.ID,
			"username": merchant.Username,
			"balance":  merchant.Balance,
		},
	})
}

// Logout 退出登录
func Logout(c *gin.Context) {
	response.Success(c, nil)
}
