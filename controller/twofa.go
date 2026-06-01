package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// Setup2FARequest 设置2FA请求结构
type Setup2FARequest struct {
	Code string `json:"code" binding:"required"`
}

// Verify2FARequest 验证2FA请求结构
type Verify2FARequest struct {
	Code string `json:"code" binding:"required"`
}

// Setup2FAResponse 设置2FA响应结构
type Setup2FAResponse struct {
	Secret      string   `json:"secret"`
	QRCodeData  string   `json:"qr_code_data"`
	BackupCodes []string `json:"backup_codes"`
}

// Setup2FA 初始化2FA设置
func Setup2FA(c *gin.Context) {
	userId := c.GetInt("id")

	// 检查用户是否已经启用2FA
	existing, err := model.GetTwoFAByUserId(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if existing != nil && existing.IsEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("user already has 2FA enabled, please disable it first before reconfiguring", "用户已启用2FA，请先禁用后重新设置"),
		})
		return
	}

	// 如果存在已禁用的2FA记录，先删除它
	if existing != nil && !existing.IsEnabled {
		if err := existing.Delete(); err != nil {
			common.ApiError(c, err)
			return
		}
		existing = nil // 重置为nil，后续将创建新记录
	}

	// 获取用户信息
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 生成TOTP密钥
	key, err := common.GenerateTOTPSecret(user.Username)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("failed to generate 2FA key", "生成2FA密钥失败"),
		})
		common.SysLog("failed to generate TOTP key: " + err.Error())
		return
	}

	// 生成备用码
	backupCodes, err := common.GenerateBackupCodes()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("failed to generate backup codes", "生成备用码失败"),
		})
		common.SysLog("failed to generate backup codes: " + err.Error())
		return
	}

	// 生成二维码数据
	qrCodeData := common.GenerateQRCodeData(key.Secret(), user.Username)

	// 创建或更新2FA记录（暂未启用）
	twoFA := &model.TwoFA{
		UserId:    userId,
		Secret:    key.Secret(),
		IsEnabled: false,
	}

	if existing != nil {
		// 更新现有记录
		twoFA.Id = existing.Id
		err = twoFA.Update()
	} else {
		// 创建新记录
		err = twoFA.Create()
	}

	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 创建备用码记录
	if err := model.CreateBackupCodes(userId, backupCodes); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("failed to save backup codes", "保存备用码失败"),
		})
		common.SysLog("failed to save backup codes: " + err.Error())
		return
	}

	// 记录操作日志
	model.RecordLog(userId, model.LogTypeSystem, common.LanguageString("started setting up two-factor authentication", "开始设置两步验证"))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": common.LanguageString("2FA setup initialized successfully, please scan the QR code with your authenticator app and enter the verification code to complete setup", "2FA设置初始化成功，请使用认证器扫描二维码并输入验证码完成设置"),
		"data": Setup2FAResponse{
			Secret:      key.Secret(),
			QRCodeData:  qrCodeData,
			BackupCodes: backupCodes,
		},
	})
}

// Enable2FA 启用2FA
func Enable2FA(c *gin.Context) {
	var req Setup2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("parameter error", "参数错误"),
		})
		return
	}

	userId := c.GetInt("id")

	// 获取2FA记录
	twoFA, err := model.GetTwoFAByUserId(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if twoFA == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("please complete 2FA initialization first", "请先完成2FA初始化设置"),
		})
		return
	}
	if twoFA.IsEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("2FA is already enabled", "2FA已经启用"),
		})
		return
	}

	// 验证TOTP验证码
	cleanCode, err := common.ValidateNumericCode(req.Code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if !common.ValidateTOTPCode(twoFA.Secret, cleanCode) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("verification code or backup code is incorrect, please retry", "验证码或备用码错误，请重试"),
		})
		return
	}

	// 启用2FA
	if err := twoFA.Enable(); err != nil {
		common.ApiError(c, err)
		return
	}

	// 记录操作日志
	model.RecordLog(userId, model.LogTypeSystem, common.LanguageString("successfully enabled two-factor authentication", "成功启用两步验证"))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": common.LanguageString("two-factor authentication enabled successfully", "两步验证启用成功"),
	})
}

// Disable2FA 禁用2FA
func Disable2FA(c *gin.Context) {
	var req Verify2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("parameter error", "参数错误"),
		})
		return
	}

	userId := c.GetInt("id")

	// 获取2FA记录
	twoFA, err := model.GetTwoFAByUserId(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if twoFA == nil || !twoFA.IsEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("user has not enabled 2FA", "用户未启用2FA"),
		})
		return
	}

	// 验证TOTP验证码或备用码
	cleanCode, err := common.ValidateNumericCode(req.Code)
	isValidTOTP := false
	isValidBackup := false

	if err == nil {
		// 尝试验证TOTP
		isValidTOTP, _ = twoFA.ValidateTOTPAndUpdateUsage(cleanCode)
	}

	if !isValidTOTP {
		// 尝试验证备用码
		isValidBackup, err = twoFA.ValidateBackupCodeAndUpdateUsage(req.Code)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}

	if !isValidTOTP && !isValidBackup {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("verification code or backup code is incorrect, please retry", "验证码或备用码错误，请重试"),
		})
		return
	}

	// 禁用2FA
	if err := model.DisableTwoFA(userId); err != nil {
		common.ApiError(c, err)
		return
	}

	// 记录操作日志
	model.RecordLog(userId, model.LogTypeSystem, common.LanguageString("disabled two-factor authentication", "禁用两步验证"))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": common.LanguageString("two-factor authentication has been disabled", "两步验证已禁用"),
	})
}

// Get2FAStatus 获取用户2FA状态
func Get2FAStatus(c *gin.Context) {
	userId := c.GetInt("id")

	twoFA, err := model.GetTwoFAByUserId(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	status := map[string]interface{}{
		"enabled": false,
		"locked":  false,
	}

	if twoFA != nil {
		status["enabled"] = twoFA.IsEnabled
		status["locked"] = twoFA.IsLocked()
		if twoFA.IsEnabled {
			// 获取剩余备用码数量
			backupCount, err := model.GetUnusedBackupCodeCount(userId)
			if err != nil {
				common.SysLog("failed to get backup code count: " + err.Error())
			} else {
				status["backup_codes_remaining"] = backupCount
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    status,
	})
}

// RegenerateBackupCodes 重新生成备用码
func RegenerateBackupCodes(c *gin.Context) {
	var req Verify2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("parameter error", "参数错误"),
		})
		return
	}

	userId := c.GetInt("id")

	// 获取2FA记录
	twoFA, err := model.GetTwoFAByUserId(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if twoFA == nil || !twoFA.IsEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("user has not enabled 2FA", "用户未启用2FA"),
		})
		return
	}

	// 验证TOTP验证码
	cleanCode, err := common.ValidateNumericCode(req.Code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	valid, err := twoFA.ValidateTOTPAndUpdateUsage(cleanCode)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if !valid {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("verification code or backup code is incorrect, please retry", "验证码或备用码错误，请重试"),
		})
		return
	}

	// 生成新的备用码
	backupCodes, err := common.GenerateBackupCodes()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("failed to generate backup codes", "生成备用码失败"),
		})
		common.SysLog("failed to generate backup codes: " + err.Error())
		return
	}

	// 保存新的备用码
	if err := model.CreateBackupCodes(userId, backupCodes); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("failed to save backup codes", "保存备用码失败"),
		})
		common.SysLog("failed to save backup codes: " + err.Error())
		return
	}

	// 记录操作日志
	model.RecordLog(userId, model.LogTypeSystem, common.LanguageString("regenerated two-factor authentication backup codes", "重新生成两步验证备用码"))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": common.LanguageString("backup codes regenerated successfully", "备用码重新生成成功"),
		"data": map[string]interface{}{
			"backup_codes": backupCodes,
		},
	})
}

// Verify2FALogin 登录时验证2FA
func Verify2FALogin(c *gin.Context) {
	var req Verify2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("parameter error", "参数错误"),
		})
		return
	}

	// 从会话中获取pending用户信息
	session := sessions.Default(c)
	pendingUserId := session.Get("pending_user_id")
	if pendingUserId == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("session has expired, please log in again", "会话已过期，请重新登录"),
		})
		return
	}
	userId, ok := pendingUserId.(int)
	if !ok {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("session data is invalid, please log in again", "会话数据无效，请重新登录"),
		})
		return
	}
	// 获取用户信息
	user, err := model.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("user does not exist", "用户不存在"),
		})
		return
	}

	// 获取2FA记录
	twoFA, err := model.GetTwoFAByUserId(user.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if twoFA == nil || !twoFA.IsEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("user has not enabled 2FA", "用户未启用2FA"),
		})
		return
	}

	// 验证TOTP验证码或备用码
	cleanCode, err := common.ValidateNumericCode(req.Code)
	isValidTOTP := false
	isValidBackup := false

	if err == nil {
		// 尝试验证TOTP
		isValidTOTP, _ = twoFA.ValidateTOTPAndUpdateUsage(cleanCode)
	}

	if !isValidTOTP {
		// 尝试验证备用码
		isValidBackup, err = twoFA.ValidateBackupCodeAndUpdateUsage(req.Code)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}

	if !isValidTOTP && !isValidBackup {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("verification code or backup code is incorrect, please retry", "验证码或备用码错误，请重试"),
		})
		return
	}

	// 2FA验证成功，清理pending会话信息并完成登录
	session.Delete("pending_username")
	session.Delete("pending_user_id")
	session.Save()

	setupLogin(user, c)
}

// Admin2FAStats 管理员获取2FA统计信息
func Admin2FAStats(c *gin.Context) {
	stats, err := model.GetTwoFAStats()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stats,
	})
}

// AdminDisable2FA 管理员强制禁用用户2FA
func AdminDisable2FA(c *gin.Context) {
	userIdStr := c.Param("id")
	userId, err := strconv.Atoi(userIdStr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("invalid user ID format", "用户ID格式错误"),
		})
		return
	}

	// 检查目标用户权限
	targetUser, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	myRole := c.GetInt("role")
	if !canManageTargetRole(myRole, targetUser.Role) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LanguageString("no permission to manage 2FA settings of users with equal or higher role", "无权操作同级或更高级用户的2FA设置"),
		})
		return
	}

	// 禁用2FA
	if err := model.DisableTwoFA(userId); err != nil {
		if errors.Is(err, model.ErrTwoFANotEnabled) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": common.LanguageString("user has not enabled 2FA", "用户未启用2FA"),
			})
			return
		}
		common.ApiError(c, err)
		return
	}

	// 记录操作日志：管理员身份通过 admin_info 传递，避免在非管理员可见的日志内容中泄露。
	adminId := c.GetInt("id")
	adminName := c.GetString("username")
	adminInfo := map[string]interface{}{
		"admin_id":       adminId,
		"admin_username": adminName,
	}
	model.RecordLogWithAdminInfo(userId, model.LogTypeManage,
		common.LanguageString("administrator has forcibly disabled user two-factor authentication", "管理员强制禁用了用户的两步验证"), adminInfo)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": common.LanguageString("user 2FA has been forcibly disabled", "用户2FA已被强制禁用"),
	})
}
