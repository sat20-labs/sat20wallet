# SAT20钱包Android生物识别功能修复规划

## 项目概述

SAT20钱包Android应用中，设置页面的指纹识别功能显示"biometric .. is not implemented on android"的错误信息。本文档提供完整的问题分析和修复方案。

## 已明确的决策

- **项目使用**：Vue 3 + TypeScript + Capacitor 7
- **生物识别插件**：@aparajita/capacitor-biometric-auth v9.0.0
- **应用名称**：SAT20 Wallet（从sat20wallet更新）
- **构建工具**：Bun + Vite
- **目标平台**：Android（主要问题平台）

## 整体规划概述

### 项目目标

修复Android平台生物识别功能"not implemented on android"错误，确保用户可以正常使用指纹或面容识别功能解锁钱包。

### 技术栈

- **前端**：Vue 3 + TypeScript + Composition API
- **移动端框架**：Capacitor 7.x
- **生物识别插件**：@aparajita/capacitor-biometric-auth v9.0.0
- **构建系统**：Bun + Vite
- **开发环境**：Android Studio + Gradle

### 主要阶段

1. **问题诊断与分析阶段**：深入分析错误根本原因
2. **技术调研与方案设计阶段**：确定最佳修复策略
3. **实施修复阶段**：执行代码和配置修改
4. **测试验证阶段**：全面测试修复效果
5. **发布部署阶段**：构建和发布修复版本

### 详细任务分解

#### 阶段1：问题诊断与分析（预计1-2天）

- **任务1.1**：错误日志收集与分析
  - 目标：收集详细的错误信息和日志
  - 输入：Android设备日志、控制台错误、用户反馈
  - 输出：完整的错误分析报告
  - 涉及文件：错误日志文件、浏览器控制台
  - 预估工作量：4小时

- **任务1.2**：插件配置检查
  - 目标：验证生物识别插件的配置和集成状态
  - 输入：capacitor.config.ts、Android配置文件
  - 输出：配置检查报告
  - 涉及文件：`capacitor.config.ts`、`android/app/build.gradle`、`android/app/src/main/AndroidManifest.xml`
  - 预估工作量：3小时

- **任务1.3**：权限和依赖验证
  - 目标：检查Android权限设置和依赖关系
  - 输入：Android清单文件、Gradle依赖配置
  - 输出：权限和依赖状态报告
  - 涉及文件：`android/app/src/main/AndroidManifest.xml`、`android/app/capacitor.build.gradle`
  - 预估工作量：3小时

#### 阶段2：技术调研与方案设计（预计2-3天）

- **任务2.1**：插件兼容性分析
  - 目标：分析@aparajita/capacitor-biometric-auth与Capacitor 7的兼容性
  - 输入：插件文档、GitHub issues、社区反馈
  - 输出：兼容性分析报告和解决方案建议
  - 涉及文件：`package.json`、插件文档
  - 预估工作量：6小时

- **任务2.2**：替代方案调研
  - 目标：评估其他生物识别插件的可行性
  - 输入：@capgo/capacitor-native-biometric、@capawesome-team/capacitor-biometrics等
  - 输出：替代方案对比分析
  - 涉及文件：技术调研文档
  - 预估工作量：8小时

- **任务2.3**：修复方案设计
  - 目标：设计具体的修复实施方案
  - 输入：问题分析结果、技术调研结果
  - 输出：详细的修复方案文档
  - 涉及文件：修复方案设计文档
  - 预估工作量：4小时

#### 阶段3：实施修复（预计3-4天）

- **任务3.1**：插件重新配置和同步
  - 目标：重新配置生物识别插件并同步到原生项目
  - 输入：修复方案文档
  - 输出：重新配置的插件文件
  - 涉及文件：`capacitor.config.ts`、`package.json`、Android原生配置
  - 预估工作量：6小时

- **任务3.2**：权限配置优化
  - 目标：优化Android权限配置以支持生物识别
  - 输入：权限分析报告
  - 输出：更新后的AndroidManifest.xml和权限配置
  - 涉及文件：`android/app/src/main/AndroidManifest.xml`
  - 预估工作量：3小时

- **任务3.3**：生物识别服务代码修复
  - 目标：修复`utils/biometric.ts`中的实现问题
  - 输入：错误分析和修复方案
  - 输出：更新后的生物识别服务代码
  - 涉及文件：`utils/biometric.ts`、`components/setting/SecuritySetting.vue`
  - 预估工作量：8小时

- **任务3.4**：Gradle配置优化
  - 目标：优化Android构建配置
  - 输入：配置分析结果
  - 输出：更新后的Gradle配置文件
  - 涉及文件：`android/app/capacitor.build.gradle`、`android/build.gradle`
  - 预估工作量：4小时

#### 阶段4：测试验证（预计2-3天）

- **任务4.1**：单元测试
  - 目标：测试生物识别服务的各个功能模块
  - 输入：修复后的代码
  - 输出：单元测试报告
  - 涉及文件：测试文件、测试报告
  - 预估工作量：6小时

- **任务4.2**：集成测试
  - 目标：在真实Android设备上测试生物识别功能
  - 输入：构建后的APK文件
  - 输出：集成测试报告和问题记录
  - 涉及文件：测试设备、测试APK
  - 预估工作量：8小时

- **任务4.3**：边界情况测试
  - 目标：测试各种边界情况和异常场景
  - 输入：测试用例文档
  - 输出：边界测试报告
  - 涉及文件：测试用例、测试报告
  - 预估工作量：4小时

#### 阶段5：发布部署（预计1-2天）

- **任务5.1**：最终构建和打包
  - 目标：构建生产版本的APK
  - 输入：修复后的源代码
  - 输出：生产版本APK文件
  - 涉及文件：构建脚本、APK文件
  - 预估工作量：4小时

- **任务5.2**：版本管理和发布
  - 目标：管理版本号并发布到应用商店
  - 输入：生产APK、版本信息
  - 输出：发布的版本和发布说明
  - 涉及文件：版本管理文件、发布说明
  - 预估工作量：3小时

## 需要进一步明确的问题

### 问题1：插件选择策略

基于调研发现，当前使用的@aparajita/capacitor-biometric-auth插件在GitHub上有类似的"not implemented on android"问题报告。

**推荐方案**：

- **方案A**：继续使用@aparajita/capacitor-biometric-auth插件，通过配置修复解决
  - 优点：当前项目已集成，API设计良好，功能完整
  - 缺点：存在兼容性问题，可能需要深入调试

- **方案B**：迁移到@capgo/capacitor-native-biometric插件
  - 优点：社区活跃，文档完善，专门针对Capacitor 6+优化
  - 缺点：需要重构现有代码，API可能不兼容

- **方案C**：迁移到@capawesome-team/capacitor-biometrics插件
  - 优点：功能最全面，商业支持，错误处理完善
  - 缺点：可能需要付费，代码重构工作量较大

**等待用户选择**：

```
请选择您偏好的方案，或提供其他建议：
[ ] 方案A：修复当前@aparajita/capacitor-biometric-auth插件
[ ] 方案B：迁移到@capgo/capacitor-native-biometric插件
[ ] 方案C：迁移到@capawesome-team/capacitor-biometrics插件
[ ] 其他方案：_______
```

### 问题2：Android权限配置

当前AndroidManifest.xml中缺少生物识别相关的权限声明。

**推荐方案**：

- **方案A**：添加基础生物识别权限
  ```xml
  <uses-permission android:name="android.permission.USE_BIOMETRIC" />
  <uses-permission android:name="android.permission.USE_FINGERPRINT" />
  ```

- **方案B**：添加完整生物识别和设备权限
  ```xml
  <uses-permission android:name="android.permission.USE_BIOMETRIC" />
  <uses-permission android:name="android.permission.USE_FINGERPRINT" />
  <uses-permission android:name="android.permission.USE_FACE_AUTH" />
  <uses-feature android:name="android.hardware.fingerprint" android:required="false"/>
  <uses-feature android:name="android.hardware.face" android:required="false"/>
  ```

**等待用户选择**：

```
请选择权限配置方案：
[ ] 方案A：仅添加基础权限
[ ] 方案B：添加完整权限和特性声明
[ ] 其他方案：_______
```

### 问题3：开发环境要求

**推荐方案**：

- **方案A**：在现有环境基础上修复
  - 开发工具：Android Studio最新版
  - SDK要求：Android API Level 21+
  - Gradle版本：与Capacitor 7兼容版本

- **方案B**：升级开发环境到最新版本
  - 确保所有工具链都是最新稳定版
  - 可能需要处理版本兼容性问题

**等待用户选择**：

```
请选择开发环境方案：
[ ] 方案A：在现有环境基础上修复
[ ] 方案B：升级到最新版本环境
[ ] 需要更多环境信息确认
```

## 风险评估

### 高风险项

1. **插件兼容性风险**：@aparajita/capacitor-biometric-auth与Capacitor 7可能存在深层次兼容性问题
   - 缓解措施：准备替代插件方案，预留重构时间

2. **用户数据迁移风险**：更换插件可能影响现有用户的生物识别凭据
   - 缓解措施：设计数据迁移策略，提供重新设置指引

### 中风险项

1. **Android版本兼容性风险**：不同Android版本的生物识别API差异
   - 缓解措施：在多个Android版本上进行测试，提供降级方案

2. **设备硬件兼容性风险**：不同设备的生物识别硬件差异
   - 缓解措施：提供设备检测和降级到密码验证的机制

### 低风险项

1. **构建环境配置风险**：Gradle配置或依赖冲突
   - 缓解措施：使用官方推荐配置，建立标准化构建流程

## 测试策略

### 测试环境

1. **开发测试环境**：Android模拟器和开发设备
2. **预生产测试环境**：多种Android设备和版本的真实设备
3. **用户验收测试**：邀请部分用户进行Beta测试

### 测试用例覆盖

1. **功能测试**：
   - 生物识别可用性检测
   - 指纹识别功能
   - 面容识别功能（如果设备支持）
   - 错误处理和降级机制

2. **兼容性测试**：
   - Android 7.0-14版本覆盖
   - 不同品牌设备兼容性
   - 不同生物识别硬件测试

3. **性能测试**：
   - 生物识别响应时间
   - 内存使用情况
   - 电池消耗影响

4. **安全测试**：
   - 生物识别数据安全性
   - 恶意攻击防护
   - 权限使用合规性

### 测试执行计划

1. **第一阶段**：开发环境功能验证（1天）
2. **第二阶段**：多设备兼容性测试（2天）
3. **第三阶段**：性能和安全测试（1天）
4. **第四阶段**：用户验收测试（1-2天）

## 交付计划

### 时间线（预计10-14个工作日）

```
Week 1:
├── Day 1-2: 问题诊断与分析
├── Day 3-5: 技术调研与方案设计

Week 2:
├── Day 1-2: 插件配置修复
├── Day 3-4: 代码实现和优化
├── Day 5: 初步测试

Week 3:
├── Day 1-2: 全面测试和问题修复
├── Day 3: 最终构建和打包
├── Day 4-5: 发布准备和文档更新
```

### 里程碑

1. **里程碑1**：问题根因分析和解决方案确定（第1周结束）
2. **里程碑2**：代码修复完成并通过单元测试（第2周结束）
3. **里程碑3**：完成集成测试并达到发布标准（第3周结束）

### 交付物

1. **代码交付物**：
   - 修复后的生物识别服务代码
   - 更新的配置文件
   - 完整的测试用例

2. **文档交付物**：
   - 问题修复报告
   - API使用文档
   - 测试报告
   - 用户指南更新

3. **构建交付物**：
   - 修复版本的APK文件
   - 发布说明
   - 版本管理记录

## 成功标准

### 技术标准

1. **功能正常性**：生物识别功能在支持的Android设备上正常工作
2. **稳定性**：崩溃率低于0.1%
3. **性能指标**：生物识别响应时间<2秒
4. **兼容性**：支持Android 7.0+版本和主流设备品牌

### 用户体验标准

1. **易用性**：用户可以顺利完成生物识别设置和使用
2. **错误处理**：提供清晰的错误提示和解决方案
3. **降级机制**：在生物识别不可用时提供密码验证选项

### 安全标准

1. **数据安全**：生物识别数据安全存储，符合Android安全标准
2. **权限合规**：权限使用符合Google Play政策要求
3. **隐私保护**：用户隐私得到充分保护

---

## 用户反馈区域

请在此区域补充您对整体规划的意见和建议：

```
用户补充内容：

---

---

---

```

**注意事项**：
1. 本规划基于当前代码分析和技术调研制定，实施过程中可能需要根据实际情况调整
2. 建议在进行插件迁移前先尝试修复当前插件的配置问题
3. 修复过程中需要保持与现有功能的兼容性，避免影响其他钱包功能
4. 建议建立完善的测试和回滚机制，确保修复过程的安全性