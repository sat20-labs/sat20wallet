import * as z from 'zod'

/**
 * 通用密码验证规则
 * 符合常见钱包插件的验证标准
 * 不强制要求特定长度，但提供合理的安全建议
 */
export const passwordSchema = z.string()
  .min(6, 'Password is required')
  .refine(
    (password) => {
      // 提供密码强度建议，但不强制要求
      const hasMinLength = password.length >= 8;
      const hasUpperCase = /[A-Z]/.test(password);
      const hasLowerCase = /[a-z]/.test(password);
      const hasNumber = /[0-9]/.test(password);
      const hasSpecialChar = /[^A-Za-z0-9]/.test(password);
      
      // 计算密码强度
      let strength = 0;
      if (hasMinLength) strength++;
      if (hasUpperCase) strength++;
      if (hasLowerCase) strength++;
      if (hasNumber) strength++;
      if (hasSpecialChar) strength++;
      
      // 允许任何非空密码，但会在UI中显示密码强度
      return true;
    },
    {
      message: 'Password should be at least 8 characters with uppercase, lowercase, number and special character for better security'
    }
  )

/**
 * 创建钱包时的密码验证规则
 * 包含确认密码字段
 */
export const createPasswordSchema = z.object({
  password: passwordSchema,
  confirmPassword: z.string().min(1, 'Please confirm your password'),
}).refine((data) => data.password === data.confirmPassword, {
  message: 'Passwords do not match',
  path: ['confirmPassword'],
})

/**
 * 导入钱包时的密码验证规则
 * 包含确认密码字段
 */
export const importPasswordSchema = createPasswordSchema

/**
 * 解锁钱包时的密码验证规则
 */
export const unlockPasswordSchema = z.object({
  password: passwordSchema,
})

/**
 * 助记词验证规则
 */
export const mnemonicSchema = z.string().min(1, 'Recovery phrase is required')

/**
 * 私钥验证规则
 */
export const privateKeySchema = z.string().min(1, 'Private key is required') 