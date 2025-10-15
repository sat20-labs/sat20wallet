#!/usr/bin/env node

/**
 * CSP Configuration Setup Script
 * 用于在不同环境间切换CSP配置
 */

const fs = require('fs');
const path = require('path');

const environments = {
  production: {
    file: 'public/_headers',
    description: '生产环境 - 安全的CSP配置'
  },
  development: {
    file: 'public/_headers.dev',
    description: '开发环境 - 宽松的CSP配置'
  }
};

function setupCSP(env = 'production') {
  const config = environments[env];
  
  if (!config) {
    console.error('❌ 未知环境:', env);
    console.log('可用环境:', Object.keys(environments).join(', '));
    process.exit(1);
  }

  const sourcePath = path.join(__dirname, '..', config.file);
  const targetPath = path.join(__dirname, '..', 'public', '_headers');

  try {
    // 检查源文件是否存在
    if (!fs.existsSync(sourcePath)) {
      console.error('❌ 配置文件不存在:', sourcePath);
      process.exit(1);
    }

    // 复制配置文件
    fs.copyFileSync(sourcePath, targetPath);
    
    console.log('✅ CSP配置已设置为:', config.description);
    console.log('📁 配置文件:', targetPath);
    
    // 显示当前CSP配置的摘要
    const content = fs.readFileSync(targetPath, 'utf8');
    const cspMatch = content.match(/Content-Security-Policy:\s*(.+)/);
    if (cspMatch) {
      console.log('🔒 CSP策略摘要:');
      const policies = cspMatch[1].split(';').map(p => p.trim()).filter(p => p);
      policies.slice(0, 5).forEach(policy => {
        console.log('   -', policy);
      });
      if (policies.length > 5) {
        console.log('   ... 还有', policies.length - 5, '个策略');
      }
    }
    
  } catch (error) {
    console.error('❌ 设置CSP配置时出错:', error.message);
    process.exit(1);
  }
}

// 命令行参数处理
const env = process.argv[2] || 'production';

console.log('🔧 SAT20 Wallet CSP配置工具');
console.log('================================');

setupCSP(env);

console.log('\n💡 使用方法:');
console.log('  生产环境: node scripts/setup-csp.js production');
console.log('  开发环境: node scripts/setup-csp.js development');
console.log('\n🚀 配置完成后请重新部署到Netlify');

