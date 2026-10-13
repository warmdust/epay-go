package payment

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"

	"github.com/example/epay-go/internal/model"
	"github.com/example/epay-go/internal/service"
	"github.com/example/epay-go/pkg/sign"
	"github.com/example/epay-go/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type LegacyCreateOrderRequest struct {
	Pid        string `form:"pid" binding:"required"`
	Type       string `form:"type"`
	OutTradeNo string `form:"out_trade_no" binding:"required"`
	NotifyURL  string `form:"notify_url" binding:"required"`
	ReturnURL  string `form:"return_url"`
	Name       string `form:"name" binding:"required"`
	Money      string `form:"money" binding:"required"`
	Sign       string `form:"sign" binding:"required"`
	SignType   string `form:"sign_type"`
	ClientType string `form:"clientip"`
	Device     string `form:"device"`
	PayMethod  string `form:"pay_method"`
}

type LegacyAPIRequest struct {
	Act        string `form:"act" binding:"required"`
	Pid        string `form:"pid" binding:"required"`
	Key        string `form:"key" binding:"required"`
	TradeNo    string `form:"trade_no"`
	OutTradeNo string `form:"out_trade_no"`
	Page       int    `form:"page"`
	Limit      int    `form:"limit"`
	RefundNo   string `form:"refund_no"`
	Amount     string `form:"money"`
	Reason     string `form:"reason"`
	NotifyURL  string `form:"notify_url"`
}

type legacyResolvedMerchant struct {
	Merchant *model.Merchant
	Pid      string
}

func LegacySubmit(c *gin.Context) {
	var req LegacyCreateOrderRequest
	if err := c.ShouldBind(&req); err != nil {
		legacyHTML(c, "参数错误: "+err.Error())
		return
	}

	orderResp, _, err := createLegacyOrder(c, &req)
	if err != nil {
		legacyHTML(c, err.Error())
		return
	}

	if orderResp.PayURL != "" {
		if orderResp.PayType == "qrcode" {
			legacyQRCodePage(c, &req, orderResp)
			return
		}
		c.Redirect(302, orderResp.PayURL)
		return
	}

	legacyHTML(c, "未获取到支付跳转地址")
}

func LegacyCreateOrder(c *gin.Context) {
	var req LegacyCreateOrderRequest
	if err := c.ShouldBind(&req); err != nil {
		legacyError(c, "参数错误: "+err.Error())
		return
	}

	orderResp, resolved, err := createLegacyOrder(c, &req)
	if err != nil {
		legacyError(c, err.Error())
		return
	}

	resp := gin.H{
		"code":     1,
		"msg":      "success",
		"pid":      resolved.Pid,
		"trade_no": orderResp.TradeNo,
	}
	attachLegacyPayFields(resp, orderResp)
	c.JSON(200, resp)
}

func LegacyAPI(c *gin.Context) {
	var req LegacyAPIRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		legacyError(c, "参数错误: "+err.Error())
		return
	}

	merchantService := service.NewMerchantService()
	orderService := service.NewOrderService()
	refundService := service.NewRefundService()
	settlementService := service.NewSettlementService()

	resolved, err := resolveLegacyMerchant(merchantService, req.Pid)
	if err != nil {
		legacyError(c, "商户不存在")
		return
	}
	merchant := resolved.Merchant
	if merchant.ApiKey != req.Key {
		legacyError(c, "密钥错误")
		return
	}

	switch req.Act {
	case "query":
		c.JSON(200, gin.H{
			"code":    1,
			"msg":     "success",
			"pid":     merchant.ID,
			"user":    merchant.Username,
			"email":   merchant.Email,
			"status":  merchant.Status,
			"balance": merchant.Balance.StringFixed(2),
		})
	case "order":
		if req.TradeNo == "" && req.OutTradeNo == "" {
			legacyError(c, "trade_no 和 out_trade_no 不能同时为空")
			return
		}

		var order *model.Order
		if req.TradeNo != "" {
			order, err = orderService.GetByTradeNo(req.TradeNo)
		} else {
			order, err = orderService.GetByOutTradeNo(merchant.ID, req.OutTradeNo)
		}
		if err != nil {
			legacyError(c, "订单不存在")
			return
		}

		status := 0
		if order.Status == model.OrderStatusPaid {
			status = 1
		}

		c.JSON(200, gin.H{
			"code":         1,
			"msg":          "success",
			"pid":          merchant.ID,
			"trade_no":     order.TradeNo,
			"out_trade_no": order.OutTradeNo,
			"type":         order.PayType,
			"name":         order.Name,
			"money":        order.Amount.StringFixed(2),
			"buyer":        order.Buyer,
			"api_trade_no": order.ApiTradeNo,
			"trade_status": status,
			"status":       status,
		})
	case "orders":
		page := req.Page
		if page <= 0 {
			page = 1
		}
		limit := req.Limit
		if limit <= 0 || limit > 100 {
			limit = 20
		}

		orders, total, err := orderService.List(page, limit, &merchant.ID, nil)
		if err != nil {
			legacyError(c, "获取订单列表失败")
			return
		}

		list := make([]gin.H, 0, len(orders))
		for _, order := range orders {
			status := 0
			if order.Status == model.OrderStatusPaid {
				status = 1
			}
			list = append(list, gin.H{
				"trade_no":     order.TradeNo,
				"out_trade_no": order.OutTradeNo,
				"type":         order.PayType,
				"name":         order.Name,
				"money":        order.Amount.StringFixed(2),
				"status":       status,
				"trade_status": status,
				"created_at":   order.CreatedAt.Format("2006-01-02 15:04:05"),
			})
		}

		c.JSON(200, gin.H{
			"code":  1,
			"msg":   "success",
			"pid":   merchant.ID,
			"total": total,
			"page":  page,
			"limit": limit,
			"data":  list,
		})
	case "settle":
		settlements, total, err := settlementService.List(1, 100, &merchant.ID, nil)
		if err != nil {
			legacyError(c, "获取结算记录失败")
			return
		}

		list := make([]gin.H, 0, len(settlements))
		for _, item := range settlements {
			list = append(list, gin.H{
				"settle_no":      item.SettleNo,
				"money":          item.Amount.StringFixed(2),
				"fee":            item.Fee.StringFixed(2),
				"actual_money":   item.ActualAmount.StringFixed(2),
				"account_type":   item.AccountType,
				"account_no":     item.AccountNo,
				"account_name":   item.AccountName,
				"status":         item.Status,
				"remark":         item.Remark,
				"created_at":     item.CreatedAt.Format("2006-01-02 15:04:05"),
			})
		}

		c.JSON(200, gin.H{
			"code":  1,
			"msg":   "success",
			"pid":   merchant.ID,
			"total": total,
			"data":  list,
		})
	case "refund":
		if req.TradeNo == "" && req.OutTradeNo == "" {
			legacyError(c, "trade_no 和 out_trade_no 不能同时为空")
			return
		}
		if req.Amount == "" {
			legacyError(c, "退款金额不能为空")
			return
		}

		tradeNo := req.TradeNo
		if tradeNo == "" {
			order, err := orderService.GetByOutTradeNo(merchant.ID, req.OutTradeNo)
			if err != nil {
				legacyError(c, "订单不存在")
				return
			}
			tradeNo = order.TradeNo
		}

		refund, err := refundService.CreateRefund(merchant.ID, &service.CreateRefundRequest{
			TradeNo:   tradeNo,
			Amount:    req.Amount,
			Reason:    req.Reason,
			NotifyURL: req.NotifyURL,
		})
		if err != nil {
			legacyError(c, err.Error())
			return
		}

		c.JSON(200, gin.H{
			"code":         1,
			"msg":          "success",
			"pid":          merchant.ID,
			"refund_no":    refund.RefundNo,
			"trade_no":     refund.TradeNo,
			"out_trade_no": req.OutTradeNo,
			"money":        refund.Amount,
			"status":       refund.Status,
		})
	default:
		legacyError(c, "暂不支持的 act: "+req.Act)
	}
}

func createLegacyOrder(c *gin.Context, req *LegacyCreateOrderRequest) (*service.CreateOrderResponse, *legacyResolvedMerchant, error) {
	merchantService := service.NewMerchantService()
	orderService := service.NewOrderService()

	resolved, err := resolveLegacyMerchant(merchantService, req.Pid)
	if err != nil {
		return nil, nil, fmt.Errorf("商户不存在")
	}
	merchant := resolved.Merchant
	if merchant.Status != 1 {
		return nil, nil, fmt.Errorf("商户已被禁用")
	}

	params := url.Values{}
	params.Set("pid", req.Pid)
	if req.Type != "" {
		params.Set("type", req.Type)
	}
	params.Set("out_trade_no", req.OutTradeNo)
	params.Set("notify_url", req.NotifyURL)
	params.Set("name", req.Name)
	params.Set("money", req.Money)
	if req.ReturnURL != "" {
		params.Set("return_url", req.ReturnURL)
	}
	if req.Device != "" {
		params.Set("device", req.Device)
	}
	if req.PayMethod != "" {
		params.Set("pay_method", req.PayMethod)
	}
	if req.ClientType != "" {
		params.Set("clientip", req.ClientType)
	}

	if !sign.VerifyMD5Sign(params, merchant.ApiKey, req.Sign) {
		return nil, nil, fmt.Errorf("签名验证失败")
	}

	amount, err := decimal.NewFromString(req.Money)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		return nil, nil, fmt.Errorf("金额格式错误")
	}

	routing, err := resolvePayRouting(req.Type, req.PayMethod)
	if err != nil {
		return nil, nil, err
	}

	orderResp, err := orderService.Create(context.Background(), &service.CreateOrderRequest{
		MerchantID:       merchant.ID,
		OutTradeNo:       req.OutTradeNo,
		Amount:           amount,
		Name:             req.Name,
		PayType:          routing.PayType,
		NotifyURL:        req.NotifyURL,
		MerchantNotifyURL: req.NotifyURL,
		PlatformBaseURL:  getPaymentBaseURL(c),
		ReturnURL:        req.ReturnURL,
		ClientIP:         utils.GetClientIP(c),
		PayMethod:        routing.PayMethod,
	})
	if err != nil {
		return nil, nil, err
	}

	return orderResp, resolved, nil
}

func resolveLegacyMerchant(merchantService *service.MerchantService, pid string) (*legacyResolvedMerchant, error) {
	if id, err := strconv.ParseInt(pid, 10, 64); err == nil && id > 0 {
		merchant, getErr := merchantService.GetByID(id)
		if getErr == nil {
			return &legacyResolvedMerchant{Merchant: merchant, Pid: pid}, nil
		}
	}

	merchant, err := merchantService.GetByAPIKey(pid)
	if err != nil {
		return nil, err
	}
	return &legacyResolvedMerchant{Merchant: merchant, Pid: strconv.FormatInt(merchant.ID, 10)}, nil
}
func attachLegacyPayFields(resp gin.H, orderResp *service.CreateOrderResponse) {
	if orderResp.PayURL == "" {
		return
	}
	resp["payurl"] = orderResp.PayURL
	switch orderResp.PayType {
	case "qrcode":
		resp["qrcode"] = orderResp.PayURL
	case "redirect":
		resp["payurl"] = orderResp.PayURL
	case "jsapi":
		resp["urlscheme"] = orderResp.PayURL
	}
}

func legacyHTML(c *gin.Context, msg string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(200, "<html><body><h3>"+msg+"</h3></body></html>")
}

func legacyQRCodePage(c *gin.Context, req *LegacyCreateOrderRequest, orderResp *service.CreateOrderResponse) {
	escapedAmount := template.HTMLEscapeString(req.Money)
	escapedTradeNo := template.HTMLEscapeString(orderResp.TradeNo)
	escapedOutTradeNo := template.HTMLEscapeString(req.OutTradeNo)
	escapedPayURLHTML := template.HTMLEscapeString(orderResp.PayURL)
	statusAPIURL := template.JSEscapeString(getPaymentBaseURL(c) + "/api/pay/status/" + orderResp.TradeNo)
	escapedReturnURL := template.HTMLEscapeString(req.ReturnURL)
	escapedReturnURLJS := template.JSEscapeString(req.ReturnURL)
	qrImageURL := template.HTMLEscapeString("https://api.qrserver.com/v1/create-qr-code/?size=240x240&data=" + url.QueryEscape(orderResp.PayURL))
	returnSection := ""
	if strings.TrimSpace(req.ReturnURL) != "" {
		returnSection = fmt.Sprintf(`<a class="btn ghost" href="%s">返回商户页面</a>`, escapedReturnURL)
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>收银台</title>
  <style>
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: #f2f3f5;
      color: #333;
      font-family: -apple-system, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
    }
    .top {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 14px 20px;
      background: #fff;
      border-bottom: 1px solid #e8e8e8;
    }
    .brand { font-size: 16px; font-weight: 600; color: #222; }
    .safe { font-size: 12px; color: #999; }
    .card {
      max-width: 360px;
      margin: 24px auto 20px;
      padding: 32px 28px;
      background: #fff;
      border-radius: 8px;
      box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
    }
    .channel {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      margin-bottom: 14px;
      font-size: 15px;
      font-weight: 600;
      color: #07c160;
    }
    .amount { text-align: center; font-size: 36px; font-weight: 600; color: #222; margin-bottom: 24px; }
    .qr {
      display: block;
      width: 240px;
      height: 240px;
      margin: 0 auto;
      padding: 8px;
      border: 1px solid #eee;
      border-radius: 4px;
    }
    .status { margin: 18px 0 2px; text-align: center; font-size: 14px; color: #666; }
    .status.ok { color: #07c160; font-weight: 600; }
    .tip { margin: 0 0 20px; text-align: center; font-size: 12px; color: #b0b0b0; }
    .btns { display: flex; gap: 10px; justify-content: center; margin-bottom: 24px; }
    .btn {
      flex: 1;
      max-width: 150px;
      padding: 10px 0;
      border-radius: 4px;
      text-align: center;
      font-size: 14px;
      text-decoration: none;
      background: #07c160;
      color: #fff;
    }
    .btn:hover { background: #06ad56; }
    .btn.ghost { background: #fff; color: #555; border: 1px solid #ddd; }
    .btn.ghost:hover { background: #f7f7f7; }
    .meta { border-top: 1px solid #f0f0f0; padding-top: 16px; }
    .row {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      margin-bottom: 8px;
      font-size: 12px;
    }
    .row .k { color: #999; flex-shrink: 0; }
    .row .v { color: #555; text-align: right; word-break: break-all; }
    .row a { color: #576b95; text-decoration: none; }
    .foot {
      max-width: 360px;
      margin: 0 auto 32px;
      text-align: center;
      font-size: 12px;
      color: #b0b0b0;
      line-height: 1.8;
    }
  </style>
</head>
<body>
  <header class="top">
    <span class="brand">收银台</span>
    <span class="safe">安全支付</span>
  </header>
  <main class="card">
    <div class="channel">
      <svg viewBox="0 0 24 24" width="20" height="20" fill="#07c160" aria-hidden="true"><path d="M8.7 4C4.9 4 1.8 6.6 1.8 9.9c0 1.9 1 3.5 2.6 4.6l-.7 2.1 2.4-1.2c.8.2 1.6.4 2.6.4h.5c-.1-.4-.2-.9-.2-1.3 0-3.1 3-5.6 6.7-5.6h.4C15.5 6 12.4 4 8.7 4zm-2.2 3c.5 0 .9.4.9.9s-.4.9-.9.9-.9-.4-.9-.9.4-.9.9-.9zm4.9 0c.5 0 .9.4.9.9s-.4.9-.9.9-.9-.4-.9-.9.4-.9.9-.9zM22 14.5c0-2.8-2.7-5-6-5s-6 2.2-6 5 2.7 5 6 5c.7 0 1.4-.1 2-.3l2 1-.6-1.7c1.6-.9 2.6-2.3 2.6-4zm-8-.6c-.4 0-.8-.3-.8-.8s.4-.8.8-.8.8.3.8.8-.4.8-.8.8zm4 0c-.4 0-.8-.3-.8-.8s.4-.8.8-.8.8.3.8.8-.4.8-.8.8z"/></svg>
      <span>微信支付</span>
    </div>
    <div class="amount">¥%s</div>
    <img class="qr" src="%s" alt="支付二维码">
    <p class="status" id="pay-status">正在等待支付结果…</p>
    <p class="tip">请使用微信「扫一扫」扫描二维码完成付款</p>
    <div class="btns">
      <a class="btn" href="%s">打开支付链接</a>
      %s
    </div>
    <div class="meta">
      <div class="row"><span class="k">平台订单号</span><span class="v">%s</span></div>
      <div class="row"><span class="k">商户订单号</span><span class="v">%s</span></div>
    </div>
  </main>
  <footer class="foot">
    支付完成后将自动跳转回商户页面，如有疑问请联系商户客服。<br>
    付款前请核对订单金额与商品信息，谨防诈骗。
  </footer>
  <script>
    const statusUrl = "%s";
    const returnUrl = "%s";
    const statusEl = document.getElementById("pay-status");
    let redirected = false;

    setInterval(async function () {
      if (redirected) return;
      try {
        const res = await fetch(statusUrl, { cache: "no-store" });
        const data = await res.json();
        if (data && data.code === 0 && data.data && data.data.paid) {
          redirected = true;
          statusEl.textContent = "支付成功，正在跳转…";
          statusEl.classList.add("ok");
          setTimeout(function () {
            window.location.href = returnUrl || location.href;
          }, 1200);
        }
      } catch (e) { /* 网络异常，等下一轮轮询 */ }
    }, 3000);
  </script>
</body>
</html>`,
		escapedAmount, qrImageURL,
		escapedPayURLHTML, returnSection,
		escapedTradeNo, escapedOutTradeNo,
		statusAPIURL, escapedReturnURLJS)

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(200, html)
}

func legacyError(c *gin.Context, msg string) {
	c.JSON(200, gin.H{
		"code": -1,
		"msg":  msg,
	})
}
