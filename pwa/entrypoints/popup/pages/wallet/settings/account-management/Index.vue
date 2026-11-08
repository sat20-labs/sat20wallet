<template>
  <LayoutScroll>
    <div class="p-4 space-y-5 max-w-2xl mx-auto">
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="icon" :aria-label="t('common.back')" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="h-5 w-5" />
        </Button>
        <div>
          <h1 class="text-xl font-semibold">{{ t('accountManagement.ui.managementTitle') }}</h1>
          <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.managementIntro') }}</p>
        </div>
      </div>

      <Alert v-if="savedState && (savedState.active || savedState.account_state === 'local' || accountManagementState.quotaNoticeKey)" :aria-label="t('accountManagement.ui.backupStatusLabel')">
        <AlertDescription class="space-y-1">
          <div>{{ t('accountManagement.ui.accountStatus', { value: t(`accountManagement.accountState.${savedState.account_state}`) }) }}</div>
          <div>{{ t('accountManagement.ui.managementStatus', { value: savedState.recovery_configured ? t('accountManagement.ui.enabled') : savedState.activation_pending ? t('accountManagement.ui.activationPending') : t('accountManagement.ui.notActivated') }) }}</div>
          <div v-if="accountManagementState.hasDatabaseIssue" role="alert">{{ t('accountManagement.readiness.databaseFailed') }}</div>
          <div v-else-if="savedState.account_state === 'local'" role="status">{{ t('accountManagement.readiness.local') }}</div>
          <div v-else-if="savedState.account_state === 'syncing'" role="status">{{ t('accountManagement.readiness.syncing') }}</div>
          <div v-else-if="savedState.account_state === 'error'" role="alert">{{ t('accountManagement.readiness.error') }}</div>
          <div v-else-if="savedState.backup_state === 'pending' || savedState.backup_state === 'uploading'" role="status">{{ t(savedState.backup_state === 'pending' ? 'accountManagement.readiness.backupPending' : 'accountManagement.readiness.backupUploading') }}</div>
          <div v-else-if="savedState.backup_state === 'cleanup'" role="status">{{ t('accountManagement.readiness.cleanup') }}</div>
          <div v-if="savedState.recovery_configured">{{ t('accountManagement.ui.storageStatus', { value: savedState.storage_mode === 'paid' ? t('accountManagement.ui.autopayStorage') : t('accountManagement.ui.nodeCache') }) }}</div>
          <div
            v-if="autopayStatus?.required && !autopayStatus.ready"
            class="mt-2 rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-amber-400"
          >
            <div class="font-medium">{{ t('accountManagement.dkvsPaymentNeedsAttention') }}</div>
            <div class="mt-1">{{ accountDisplayMessage(t, autopayStatus.message) || t('accountManagement.ui.autopayUnavailable') }}</div>
            <div v-if="autopayStatus.current_block" class="mt-1 text-amber-400/80">
              {{ t('accountManagement.ui.currentHeight', { value: autopayStatus.current_block }) }}
              <span v-if="autopayStatus.last_pay_height">{{ t('accountManagement.ui.lastPaidHeight', { value: autopayStatus.last_pay_height }) }}</span>
            </div>
            <Button
              v-if="autopayStatus.can_fund"
              size="sm"
              class="mt-2"
              :disabled="busy"
              @click="fundAutopay"
            >
              <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
              {{ t('accountManagement.dkvsPay') }}
            </Button>
          </div>
          <div
            v-else-if="autopayStatus?.required && autopayStatus.ready"
            class="text-green-500"
          >
            {{ t('accountManagement.dkvsPaymentNormal') }}
            <Button v-if="autopayStatus.can_fund" size="sm" variant="outline" class="ml-2" :disabled="busy" @click="fundAutopay">
              {{ t('accountManagement.dkvsRenew') }}
            </Button>
          </div>

          <div v-if="autopayStatus?.required" :aria-label="t('accountManagement.ui.paidHeightLabel')" class="mt-2 space-y-1 text-sm">
            <div v-if="autopayStatus.current_block">{{ t('accountManagement.dkvsCurrentHeight', { height: autopayStatus.current_block }) }}</div>
            <div v-if="autopayStatus.last_pay_height">{{ t('accountManagement.dkvsPaidHeight', { height: autopayStatus.last_pay_height }) }}</div>
            <div v-else>{{ t('accountManagement.dkvsPaidHeightUnknown') }}</div>
            <div v-if="autopayStatus.amount_per_block">{{ t('accountManagement.dkvsBalanceAndRate', { balance: autopayStatus.balance || '0', rate: autopayStatus.amount_per_block, asset: autopayStatus.fee_asset }) }}</div>
            <div v-if="autopayStatus.estimated_expiry_height">{{ t('accountManagement.dkvsExpiryEstimate', { height: autopayStatus.estimated_expiry_height }) }}</div>
            <div v-else class="text-muted-foreground">{{ t('accountManagement.dkvsExpiryUnavailable') }}</div>
            <p class="text-xs text-muted-foreground">{{ t('accountManagement.dkvsExpiryCaveat') }}</p>
          </div>

          <div>{{ t('accountManagement.ui.recoveryConfiguration', { value: savedState.recovery_configured ? t('accountManagement.ui.configured') : t('accountManagement.ui.notConfigured') }) }}</div>
          <div v-if="savedState.recovery_configured">{{ t('accountManagement.ui.recoveryMode', { value: savedState.recovery_mode }) }}</div>
          <div>{{ t('accountManagement.ui.dataRevision', { value: savedState.managed_data_revision || 0 }) }}</div>
          <div>{{ t('accountManagement.ui.writeEligibility', { value: t(savedState.write_allowed ? 'accountManagement.dkvsWriteAllowed' : 'accountManagement.dkvsReadOnly') }) }}</div>
          <div>{{ t('accountManagement.ui.backupState', { value: t(`accountManagement.backupState.${savedState.backup_state}`) }) }}</div>
          <p v-if="accountManagementState.quotaNoticeKey" role="alert" class="mt-1 text-amber-500">
            {{ t(accountManagementState.quotaNoticeKey) }}
          </p>
          <div v-for="module in savedState.modules || []" :key="`${module.provider}:${module.scope}`">
            {{ t('accountManagement.ui.moduleState', { provider: module.provider, status: t(`accountManagement.backupState.${module.backup_state}`) }) }}
            <p v-if="module.last_error" class="mt-1 break-words text-sm text-muted-foreground">
              {{ accountDisplayMessage(t, module.last_error) }}
            </p>
          </div>
          <div v-if="savedState.managed_data_dirty && savedState.recovery_configured" class="text-amber-500">{{ t('accountManagement.ui.unbackedNotice') }}</div>
          <div v-if="savedState.last_rehearsal_at">{{ t('accountManagement.ui.lastRehearsal', { value: new Date(savedState.last_rehearsal_at).toLocaleString() }) }}</div>
          <div>{{ t('accountManagement.ui.pendingChanges', { value: savedState.pending_changes || 0 }) }}</div>
          <div v-if="savedState.recovery_configured">{{ t('accountManagement.dkvsStatusLabel', { status: t(`accountManagement.dkvsSyncStatus.${savedState.dkvs_status || 'unknown'}`) }) }}</div>
          <div v-if="savedState.recovery_configured">{{ t('accountManagement.dkvsWriteLabel', { status: t(savedState.dkvs_write_allowed === true ? 'accountManagement.dkvsWriteAllowed' : savedState.dkvs_write_allowed === false ? 'accountManagement.dkvsReadOnly' : 'accountManagement.dkvsWriteUnknown') }) }}</div>
          <div v-if="savedState.dkvs_cache_at_ms">
            {{ t('accountManagement.dkvsCacheVerifiedAt', { time: new Date(savedState.dkvs_cache_at_ms).toLocaleString() }) }}<span v-if="savedState.dkvs_cache_height !== undefined">{{ t('accountManagement.dkvsCacheHeight', { height: savedState.dkvs_cache_height }) }}</span>
          </div>
          <div>{{ t('accountManagement.dkvsPendingRequestsLabel', { count: savedState.dkvs_pending_requests ?? 0 }) }}</div>
          <div
            v-if="savedState.last_dkvs_sync_error"
            class="mt-2 rounded-md border border-red-500/40 bg-red-500/10 p-2 text-red-400"
          >
            <div class="font-medium">{{ t('accountManagement.ui.lastDiagnostic', { value: savedState.last_dkvs_sync_error_code || 'DKVS_SYNC_ERROR' }) }}</div>
            <div class="mt-1 break-words">{{ accountDisplayMessage(t, savedState.last_dkvs_sync_error) }}</div>
            <div v-if="savedState.last_dkvs_sync_error_at" class="mt-1 text-red-400/80">
              {{ t('accountManagement.ui.lastFailure', { value: new Date(savedState.last_dkvs_sync_error_at).toLocaleString() }) }}
            </div>
          </div>
          <Button
            v-if="!accountManagementState.hasDatabaseIssue && savedState.recovery_configured && (savedState.account_state === 'error' || ['pending', 'uploading', 'cleanup'].includes(savedState.backup_state) || accountManagementState.moduleIssues.length > 0 || accountManagementState.hasQuotaIssue)"
            variant="outline"
            size="sm"
            :disabled="busy"
            class="mt-2"
            @click="retryDKVSSync"
          >{{ t('accountManagement.readiness.retryNow') }}</Button>
        </AlertDescription>
      </Alert>
      <p v-if="statusReadError" class="text-sm text-destructive" role="status">{{ statusReadError }}</p>
      <Button v-if="statusReadError" variant="outline" :disabled="busy" @click="refreshAccountStatus">{{ t('accountManagement.readiness.retryReadStatus') }}</Button>

      <section v-if="savedState?.recovery_configured" class="space-y-3 rounded-lg border p-3" :aria-label="t('accountManagement.ui.cacheViewerLabel')">
        <div class="flex items-center justify-between gap-2">
          <div>
            <h2 class="font-medium">{{ t('accountManagement.ui.cacheTitle') }}</h2>
            <p class="text-xs text-muted-foreground">{{ t('accountManagement.ui.cacheIntro') }}</p>
          </div>
          <Button variant="outline" size="sm" :disabled="viewerLoading" @click="loadDKVSInspection()">
            {{ viewerLoading ? t('accountManagement.ui.loading') : t('accountManagement.ui.readCache') }}
          </Button>
        </div>
        <p v-if="viewerError" role="status" class="text-sm text-destructive break-words">{{ viewerError }}</p>
        <template v-if="viewer">
          <div class="space-y-1 text-xs text-muted-foreground break-all">
            <div>{{ t('accountManagement.ui.accountId', { value: viewer.scope.account_id }) }}</div>
            <div>{{ t('accountManagement.ui.snapshotId', { value: viewer.snapshot.id }) }}</div>
            <div>{{ t('accountManagement.ui.cacheSummary', { status: viewer.snapshot.status, count: viewer.snapshot.total_cached_count }) }}</div>
            <div v-if="viewer.snapshot.verified_at_ms">{{ t('accountManagement.ui.cacheVerified', { value: new Date(viewer.snapshot.verified_at_ms).toLocaleString() }) }}<span v-if="viewer.snapshot.cache_height">{{ t('accountManagement.ui.cacheHeight', { value: viewer.snapshot.cache_height }) }}</span></div>
            <div v-if="viewer.scope.wallet_subscopes?.length">{{ t('accountManagement.ui.walletScopes', { value: viewer.scope.wallet_subscopes.join(t('accountManagement.ui.listSeparator')) }) }}</div>
            <div v-if="!viewer.snapshot.complete" class="text-amber-500">{{ t('accountManagement.ui.incompleteCache') }}</div>
            <div v-if="!viewer.snapshot.remote_current" class="text-amber-500">{{ t('accountManagement.ui.remoteUnverified') }}</div>
            <div v-if="viewer.snapshot.local_dirty || viewer.snapshot.pending_changes" class="text-amber-500">{{ t('accountManagement.ui.cacheDirty') }}</div>
          </div>
          <div class="max-h-36 overflow-y-auto space-y-1" :aria-label="t('accountManagement.ui.recordList')">
            <button
              v-for="record in viewer.records" :key="record.key" type="button"
              class="block w-full rounded border p-2 text-left text-xs break-all"
              :class="selectedViewerKey === record.key ? 'border-primary' : 'border-muted'"
              @click="selectedViewerKey = record.key"
            >{{ record.key }}</button>
            <p v-if="!viewer.records.length" class="text-xs text-muted-foreground">{{ t('accountManagement.ui.noRecords') }}</p>
          </div>
          <div v-if="selectedViewerRecord" class="grid gap-3 md:grid-cols-2 text-xs">
            <div class="min-w-0 rounded border p-2" :aria-label="t('accountManagement.ui.rawHexLabel')">
              <div class="font-medium">{{ t('accountManagement.ui.rawHex') }}</div>
              <div class="mt-1 text-muted-foreground break-all">{{ t('accountManagement.ui.snapshotRecord', { snapshot: viewer.snapshot.id, record: selectedViewerRecord.record_hash }) }}</div>
              <pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all">{{ selectedViewerRecord.raw_hidden ? t('accountManagement.ui.hiddenRaw', { reason: selectedViewerRecord.raw_hidden_reason || 'unverified_value_type' }) : selectedViewerRecord.raw_hex || t('accountManagement.ui.emptyValue') }}</pre>
            </div>
            <div class="min-w-0 rounded border p-2" :aria-label="t('accountManagement.ui.safeDecodeLabel')">
              <div class="font-medium">{{ t('accountManagement.ui.safeDecode') }}</div>
              <div class="mt-1 text-muted-foreground break-all">{{ t('accountManagement.ui.snapshotRecord', { snapshot: viewer.snapshot.id, record: selectedViewerRecord.record_hash }) }}</div>
              <pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all">{{ JSON.stringify(selectedViewerRecord.decoded, null, 2) }}</pre>
            </div>
          </div>
          <Button v-if="viewer.next_cursor" variant="outline" size="sm" :disabled="viewerLoading" @click="loadDKVSInspection(viewer.next_cursor)">{{ t('accountManagement.ui.nextPage') }}</Button>
        </template>
      </section>

      <section v-if="step === 1 && savedState?.recovery_configured && !reconfiguring" class="space-y-3">
        <h2 class="font-medium">{{ t('accountManagement.ui.maintainRecovery') }}</h2>
        <Textarea v-if="savedState.public_locator" :model-value="savedState.public_locator" readonly rows="5" :aria-label="t('accountManagement.ui.currentLocator')" />
        <Button variant="outline" class="w-full" :disabled="busy || !savedState.public_locator" @click="run(async () => { await copyText(savedState!.public_locator || '') })">{{ t('accountManagement.ui.copyCurrentLocator') }}</Button>
        <Button variant="outline" class="w-full" :disabled="busy || !savedState.public_locator" @click="router.push('/restore-account?mode=rehearsal')">{{ t('accountManagement.ui.rehearsalTitle') }}</Button>
        <p class="text-xs text-muted-foreground">{{ t('accountManagement.ui.rehearsalHint') }}</p>
        <Button class="w-full" :disabled="busy" @click="reconfiguring = true">{{ t('accountManagement.ui.reconfigure') }}</Button>
      </section>

      <section v-if="autopayStatus?.funding_transaction_id || autopayFundingResult?.transaction_id" :aria-label="t('accountManagement.ui.fundingTracking')" class="rounded-lg border p-3 space-y-2 text-sm">
        <p>{{ accountDisplayMessage(t, autopayStatus?.funding_error) || (autopayStatus?.ready && !autopayStatus?.funding_pending ? t('accountManagement.ui.fundingReady') : (autopayStatus?.funding_pending ?? autopayFundingResult?.pending) ? t('accountManagement.ui.fundingPending') : t('accountManagement.ui.fundingNotReady')) }}</p>
        <p class="break-all">{{ t('accountManagement.ui.transactionId', { value: autopayStatus?.funding_transaction_id || autopayFundingResult?.transaction_id }) }}</p>
        <p v-if="autopayStatus?.funding_pending" class="text-xs text-muted-foreground">{{ t('accountManagement.ui.checkOriginalFunding') }}</p>
        <Button variant="outline" :disabled="busy" @click="run(refreshAutopayStatus)">{{ t('accountManagement.ui.checkFunding') }}</Button>
        <Button v-if="autopayStatus?.funding_pending && autopayStatus?.funding_can_resume" variant="outline" :disabled="busy" @click="resumeAutopay">{{ t('accountManagement.ui.resumeFunding') }}</Button>
      </section>

      <Alert v-if="reconfiguring">
        <AlertDescription>{{ t('accountManagement.ui.reconfigureWarning') }}</AlertDescription>
      </Alert>
      <Button v-if="reconfiguring && step < 4" variant="ghost" class="w-full" :disabled="busy" @click="cancelReconfiguration">{{ t('accountManagement.ui.backToConfiguration') }}</Button>

      <section v-if="step === 1 && (!savedState?.recovery_configured || reconfiguring)" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.stepWallets') }}</h2>
        <p class="text-sm text-muted-foreground">
          {{ t('accountManagement.ui.walletBackupHint') }}
        </p>
        <div v-for="wallet in wallets" :key="wallet.id" class="rounded-lg border p-3 space-y-3">
          <div class="font-medium">{{ wallet.name }}</div>
          <div v-for="account in wallet.accounts" :key="account.index" class="space-y-1">
            <label class="text-xs text-muted-foreground">{{ t('accountManagement.ui.subaccountDid', { index: account.index }) }}</label>
            <Input :model-value="account.did || ''" readonly :aria-label="t('accountManagement.ui.savedSubaccountDid', { index: account.index })" />
          </div>
        </div>
        <Button variant="outline" class="w-full" :disabled="busy" @click="router.push('/wallet/name-select')">{{ t('accountManagement.ui.setDid') }}</Button>
        <Button class="w-full" :disabled="busy" @click="runPreflight">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          {{ t('accountManagement.ui.checkAccount') }}
        </Button>
      </section>

      <section v-else-if="step === 2" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.stepStorage') }}</h2>
        <div class="grid grid-cols-2 gap-2">
          <Button :variant="recoveryMode === '2of3' ? 'default' : 'outline'" @click="recoveryMode = '2of3'">
            {{ t('accountManagement.ui.modeConvenient') }}
          </Button>
          <Button :variant="recoveryMode === '2of2' ? 'default' : 'outline'" @click="recoveryMode = '2of2'">
            {{ t('accountManagement.ui.modeSecure') }}
          </Button>
        </div>
        <p class="text-xs text-muted-foreground">
          {{ t('accountManagement.ui.recoveryModesHint') }}
        </p>
        <div class="space-y-2">
          <button
            v-for="option in storageOptions"
            :key="option.id"
            type="button"
            class="w-full text-left rounded-lg border p-3 disabled:opacity-50"
            :class="selectedStorage === option.id ? 'border-primary' : ''"
            :disabled="!option.available"
            @click="selectStorage(option)"
          >
            <div class="font-medium">{{ accountDisplayMessage(t, option.title) }}</div>
            <div class="text-sm text-muted-foreground">{{ accountDisplayMessage(t, option.description) }}</div>
            <div v-if="option.estimated_cost" class="text-xs mt-1">
              {{ t('accountManagement.ui.storageQuote', { label: t(option.id === 'paid' ? 'accountManagement.ui.defaultQuote' : 'accountManagement.ui.currentQuote'), cost: option.estimated_cost, asset: option.fee_asset, annual: option.estimated_annual_cost }) }}
            </div>
            <div v-if="option.ttl_blocks" class="text-xs mt-1">
              {{ t('accountManagement.ui.retentionBlocks', { value: option.ttl_blocks }) }}
              <span v-if="option.estimated_expiry_height">{{ t('accountManagement.ui.estimatedExpiry', { value: option.estimated_expiry_height }) }}</span>
            </div>
            <ul v-if="option.warnings?.length" class="text-xs text-amber-500 mt-1 list-disc pl-4">
              <li v-for="warning in option.warnings" :key="warning">{{ accountDisplayMessage(t, warning) }}</li>
            </ul>
          </button>
        </div>
        <div v-if="selectedStorage === 'paid'" class="space-y-2">
          <label class="text-sm font-medium" for="autopay-record-count">{{ t('accountManagement.ui.recordCount') }}</label>
          <Input
            id="autopay-record-count"
            v-model.number="recordCount"
            type="number"
            inputmode="numeric"
            min="100"
            step="1"
          />
          <p class="text-xs text-muted-foreground">
            {{ t('accountManagement.ui.rateHint', { rate: paidStorageOption?.full_record_fee_per_block || '-', asset: paidStorageOption?.fee_asset || '' }) }}
          </p>
          <p v-if="autopayQuote" class="text-xs text-muted-foreground">
            {{ t('accountManagement.ui.fundingQuote', { rate: autopayQuote.amountPerBlock, asset: paidStorageOption?.fee_asset, initial: autopayQuote.initialCost, annual: autopayQuote.annualCost }) }}
          </p>
        </div>
        <Button class="w-full" :disabled="busy || !selectedStorage || !recordCountValid" @click="confirmStorage">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          {{ t('accountManagement.ui.confirmStorage') }}
        </Button>
        <Button
          v-if="selectedStorage === 'paid'"
          variant="outline"
          class="w-full"
          :disabled="busy || !recordCountValid"
          @click="reusePaidStorage"
        >
          {{ t('accountManagement.ui.reuseAutopay') }}
        </Button>
      </section>

      <section v-else-if="step === 3" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.stepQuestions') }}</h2>
        <Alert>
          <AlertDescription>
            {{ t('accountManagement.ui.questionHint') }}
          </AlertDescription>
        </Alert>
        <div v-for="(question, index) in questions" :key="question.id" class="rounded-lg border p-3 space-y-2">
          <label class="text-sm font-medium">{{ t('accountManagement.ui.questionNumber', { value: index + 1 }) }}</label>
          <Input v-model="question.prompt" />
          <Input v-model="question.answer" type="password" :placeholder="t('accountManagement.ui.answer')" autocomplete="off" />
          <Input v-model="question.confirmation" type="password" :placeholder="t('accountManagement.ui.repeatAnswer')" autocomplete="off" />
        </div>
        <div v-if="recoveryMode === '2of3'" class="space-y-2">
          <label class="text-sm font-medium">{{ t('accountManagement.ui.guardianContact') }}</label>
          <Textarea v-model="guardianContact" rows="5" :placeholder="t('accountManagement.ui.pasteGuardianContact')" />
          <p class="text-xs text-muted-foreground">{{ t('accountManagement.ui.guardianContactHint') }}</p>
          <p v-if="guardianContact.trim() && guardianContactError" class="text-xs text-destructive">{{ guardianContactError }}</p>
        </div>
        <Button class="w-full" :disabled="busy || !guardianContactValid" @click="createRecovery">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          {{ t('accountManagement.ui.createBackup') }}
        </Button>
        <Button variant="outline" class="w-full" :disabled="busy" @click="cancelStorageSetup">
          {{ t('accountManagement.ui.cancelSetup') }}
        </Button>
      </section>

      <section v-else-if="step === 4 && creation" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.stepSave') }}</h2>
        <div v-if="recoveryMode === '2of2' || guardianSetupVerified" class="space-y-2">
          <label class="text-sm font-medium">{{ t('accountManagement.ui.publicLocator') }}</label>
          <Textarea :model-value="creation.locator" readonly rows="5" :aria-label="t('accountManagement.ui.finalLocator')" />
          <Button variant="outline" class="w-full" @click="copyText(creation.locator)">{{ t('accountManagement.ui.copyLocator') }}</Button>
        </div>
        <div class="space-y-2">
          <label class="text-sm font-medium">{{ t('accountManagement.ui.secretShare') }}</label>
          <Textarea :model-value="creation.user_share" readonly rows="5" :aria-label="t('accountManagement.ui.secretShare')" />
          <Button variant="outline" class="w-full" @click="copyText(creation.user_share)">{{ t('accountManagement.ui.copyShare') }}</Button>
          <p class="text-xs text-muted-foreground">{{ t('accountManagement.ui.shareWarning') }}</p>
        </div>

        <div v-if="recoveryMode === '2of3' && !guardianSetupVerified" class="space-y-2 rounded-lg border p-3">
          <label class="text-sm font-medium">{{ t('accountManagement.ui.askGuardianAccept') }}</label>
          <p class="text-xs text-muted-foreground">{{ t('accountManagement.ui.guardianReceiptHint') }}</p>
          <Textarea :model-value="creation.guardian_setup" readonly rows="7" :aria-label="t('accountManagement.ui.guardianSetup')" />
          <Button variant="outline" class="w-full" @click="copyText(creation.guardian_setup)">{{ t('accountManagement.ui.copyGuardianSetup') }}</Button>
          <Textarea v-model="guardianReceipt" rows="5" :placeholder="t('accountManagement.ui.pasteReceipt')" />
          <Button class="w-full" :disabled="busy || !guardianReceipt" @click="verifyGuardian">
            {{ t('accountManagement.ui.verifyStored') }}
          </Button>
        </div>
        <Button v-else class="w-full" @click="step = 5">{{ recoveryMode === '2of3' ? t('accountManagement.ui.savedStartRehearsal') : t('accountManagement.ui.startRehearsal') }}</Button>
      </section>

      <section v-else-if="step === 5 && creation" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.stepRehearsal') }}</h2>
        <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.rehearsalInstructions') }}</p>
        <Input v-for="(answer, index) in rehearsalAnswers" :key="index" v-model="rehearsalAnswers[index]" type="password" :placeholder="t('accountManagement.ui.questionAnswer', { index: index + 1 })" autocomplete="off" />
        <Textarea v-if="recoveryMode === '2of2'" v-model="rehearsalUserShare" rows="5" :placeholder="t('accountManagement.ui.pasteShareAgain')" />
        <div v-else class="space-y-2 rounded-lg border p-3">
          <Textarea v-model="rehearsalLocator" rows="5" :placeholder="t('accountManagement.ui.pasteFinalLocatorAgain')" />
          <Button variant="outline" class="w-full" :disabled="busy" @click="createRehearsalGuardianRequest">{{ t('accountManagement.ui.createRehearsalRequest') }}</Button>
          <Textarea v-if="rehearsalGuardianRequest" :model-value="rehearsalGuardianRequest" readonly rows="6" />
          <Button v-if="rehearsalGuardianRequest" variant="ghost" class="w-full" @click="copyText(rehearsalGuardianRequest)">{{ t('accountManagement.ui.copyRehearsalRequest') }}</Button>
          <Textarea v-model="rehearsalGuardianResponse" rows="6" :placeholder="t('accountManagement.ui.pasteGuardianResponse')" />
          <Button variant="outline" class="w-full" :disabled="busy || !rehearsalGuardianResponse" @click="acceptRehearsalGuardianResponse">{{ t('accountManagement.ui.verifyGuardianResponse') }}</Button>
          <p class="text-xs text-muted-foreground">{{ t('accountManagement.ui.guardianRehearsalWarning') }}</p>
        </div>
        <Button class="w-full" :disabled="busy || (recoveryMode === '2of3' && !rehearsalGuardianReady)" @click="rehearse">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          {{ t('accountManagement.ui.runRehearsal') }}
        </Button>
      </section>

      <section v-else-if="step === 6" class="space-y-3 text-center py-8">
        <Icon icon="lucide:badge-check" class="h-14 w-14 text-green-500 mx-auto" />
        <h2 class="text-xl font-semibold">{{ t('accountManagement.ui.recoveryConfigured') }}</h2>
        <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.configurationVerified') }}</p>
        <Textarea :model-value="creation?.locator || savedState?.public_locator || ''" readonly rows="5" :aria-label="t('accountManagement.ui.currentLocator')" />
        <Button variant="outline" :disabled="busy || !creation?.locator && !savedState?.public_locator" @click="run(async () => { await copyText(creation?.locator || savedState?.public_locator || '') })">{{ t('accountManagement.ui.copyCurrentLocator') }}</Button>
        <Button @click="router.push('/wallet')">{{ t('accountManagement.ui.backToWallet') }}</Button>
      </section>

      <Separator />

      <section class="space-y-3">
        <h2 class="font-medium">{{ t('accountManagement.ui.guardianTools') }}</h2>
        <p class="text-xs text-muted-foreground">{{ t('accountManagement.ui.guardianToolsHint') }}</p>
        <Button variant="outline" class="w-full" :disabled="busy" @click="generateGuardianIdentity">{{ t('accountManagement.ui.generateContact') }}</Button>
        <div v-if="guardianIdentity" class="space-y-2">
          <Textarea :model-value="guardianIdentity" readonly rows="5" />
          <Button variant="outline" class="w-full" :disabled="busy" @click="copyGuardianIdentity">
            {{ guardianIdentityCopied ? t('accountManagement.ui.contactCopied') : t('accountManagement.ui.copyContact') }}
          </Button>
        </div>

        <Textarea v-model="guardianSetupInput" rows="5" :placeholder="t('accountManagement.ui.pasteFriendSetup')" />
        <select v-model="guardianStorageChoice" class="w-full rounded-md border bg-background p-2 text-sm">
          <option value="">{{ t('accountManagement.ui.chooseGuardianStorage') }}</option>
          <option v-for="option in storageOptions.filter(o => o.available)" :key="option.id" :value="option.id">{{ accountDisplayMessage(t, option.title) }}</option>
        </select>
        <p v-if="guardianStorageChoice === 'paid'" class="text-xs text-amber-500">{{ t('accountManagement.ui.guardianPaidHint') }}</p>
        <p v-else-if="guardianStorageChoice === 'temporary'" class="text-xs text-amber-500">{{ t('accountManagement.ui.guardianTemporaryHint') }}</p>
        <Button variant="outline" class="w-full" :disabled="busy || !guardianSetupInput || !guardianStorageChoice" @click="acceptGuardianSetup">{{ t('accountManagement.ui.acceptShare') }}</Button>
        <Textarea v-if="guardianReceiptOutput" :model-value="guardianReceiptOutput" readonly rows="5" />

        <Textarea v-model="guardianRecoveryRequest" rows="5" :placeholder="t('accountManagement.ui.pasteFriendRequest')" />
        <Button variant="outline" class="w-full" :disabled="busy || !guardianRecoveryRequest" @click="createGuardianResponse">{{ t('accountManagement.ui.createResponse') }}</Button>
        <Textarea v-if="guardianResponse" :model-value="guardianResponse" readonly rows="5" />
      </section>

      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
    </div>

    <Dialog :open="paidConfirmOpen" @update:open="handlePaidConfirmOpenChange">
      <DialogContent class="max-h-[90vh] max-w-[92vw] overflow-y-auto rounded-sm border-border bg-background sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ t('tools.txConfirm.title') }}</DialogTitle>
          <DialogDescription>{{ t('tools.txConfirm.description') }}</DialogDescription>
        </DialogHeader>
        <div class="space-y-2 rounded-sm border border-border bg-muted/30 p-3 text-sm">
          <div v-for="row in paidConfirmRows" :key="row.label" class="grid grid-cols-[88px_1fr] gap-3">
            <span class="text-muted-foreground">{{ row.label }}</span>
            <span class="break-all font-medium">{{ row.value }}</span>
          </div>
        </div>
        <DialogFooter class="grid grid-cols-2 gap-2 sm:flex sm:justify-end">
          <Button variant="outline" @click="resolvePaidConfirmation(false)">{{ t('common.cancel') }}</Button>
          <Button @click="resolvePaidConfirmation(true)">{{ t('common.confirm') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </LayoutScroll>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useWalletStore } from '@/store'
import { useAccountManagementStore } from '@/store/accountManagement'
import { withWalletPassword } from '@/lib/walletPasswordPrompt'
import accountSDK, {
  accountDisplayMessage,
  type AccountDKVSInspection,
  type AccountAutopayFundingResult,
  AccountAutopayPendingError,
  type AccountAutopayFundingStatus,
  type AccountStorageOption,
  type AccountWalletMetadataInput,
} from '@/utils/accountManagement'

const router = useRouter()
const { t } = useI18n()
const walletStore = useWalletStore()
const { wallets } = storeToRefs(walletStore)
const accountManagementState = useAccountManagementStore()
const { status: savedState } = storeToRefs(accountManagementState)
const statusReadError = computed(() => accountManagementState.statusReadFailed ? t('accountManagement.dkvsStatusReadFailed') : '')
const viewer = ref<AccountDKVSInspection | null>(null)
const viewerLoading = ref(false)
const viewerError = ref('')
const selectedViewerKey = ref('')
const selectedViewerRecord = computed(() => viewer.value?.records.find(record => record.key === selectedViewerKey.value))
const autopayStatus = ref<AccountAutopayFundingStatus | null>(null)
const autopayFundingResult = ref<AccountAutopayFundingResult | null>(null)
const busy = ref(false)
const error = ref('')
const step = ref(1)
const reconfiguring = ref(false)
const recoveryMode = ref<'2of2' | '2of3'>('2of3')
const storageOptions = ref<AccountStorageOption[]>([])
const selectedStorage = ref('')
const recordCount = ref(100)
const preflight = ref<any>(null)
const paidConfirmOpen = ref(false)
const paidConfirmContext = ref<'account' | 'guardian' | 'funding'>('account')
const fundingConfirmation = ref<AccountAutopayFundingStatus | null>(null)
let paidConfirmResolver: ((confirmed: boolean) => void) | null = null
let autopayRefreshTimer: ReturnType<typeof setInterval> | undefined
let disposed = false
const guardianContact = ref('')
const creation = ref<any>(null)
const guardianReceipt = ref('')
const guardianSetupVerified = ref(false)
const rehearsalLocator = ref('')
const rehearsalAnswers = ref(['', '', ''])
const rehearsalUserShare = ref('')
const rehearsalGuardianRequest = ref('')
const rehearsalGuardianResponse = ref('')
const rehearsalGuardianReady = ref(false)

const questions = ref([
  { id: 'book-page', prompt: t('accountManagement.ui.bookQuestion'), answer: '', confirmation: '', ignore_punctuation: true },
  { id: 'private-phrase', prompt: t('accountManagement.ui.phraseQuestion'), answer: '', confirmation: '', ignore_punctuation: true },
  { id: 'private-note', prompt: t('accountManagement.ui.noteQuestion'), answer: '', confirmation: '', ignore_punctuation: true },
])

const guardianIdentity = ref('')
const guardianIdentityCopied = ref(false)
const guardianSetupInput = ref('')
const guardianStorageChoice = ref('')
const guardianReceiptOutput = ref('')
const guardianRecoveryRequest = ref('')
const guardianResponse = ref('')

type GuardianContact = {
  network: string
  mailbox_id: string
  recovery_public_key: string
}

const parseGuardianContact = (value: string): GuardianContact | null => {
  try {
    const contact = JSON.parse(value)
    if (!contact || typeof contact !== 'object'
      || typeof contact.network !== 'string' || !contact.network
      || typeof contact.mailbox_id !== 'string' || !contact.mailbox_id
      || typeof contact.recovery_public_key !== 'string' || !contact.recovery_public_key) {
      return null
    }
    return contact as GuardianContact
  } catch {
    return null
  }
}

const parsedGuardianContact = computed(() => parseGuardianContact(guardianContact.value.trim()))
const guardianContactError = computed(() => {
  if (!guardianContact.value.trim()) return t('accountManagement.ui.contactRequired')
  if (!parsedGuardianContact.value) return t('accountManagement.ui.invalidContact')
  const rootAccountId = preflight.value?.account_id || savedState.value?.account_id || walletStore.rootAccountId
  if (rootAccountId && rootAccountId.toLowerCase() === parsedGuardianContact.value.mailbox_id.toLowerCase()) {
    return t('accountManagement.ui.selfGuardian')
  }
  return ''
})
const guardianContactValid = computed(() => recoveryMode.value !== '2of3' || !guardianContactError.value)

// The recovery wizard never supplies draft overrides. Both preflight and
// recovery export read the authoritative SDK catalog.
const metadata = (): AccountWalletMetadataInput[] => []

const run = async (task: () => Promise<void>) => {
  busy.value = true
  error.value = ''
  try { await task() } catch (e: any) {
    if (e instanceof AccountAutopayPendingError) {
      autopayFundingResult.value = e.data as AccountAutopayFundingResult
      try { await refreshAutopayStatus() } catch { /* preserve the known transaction receipt */ }
    } else {
      const quotaMessageKey = accountManagementState.quotaMessageKeyForCode(e?.code)
      error.value = quotaMessageKey
        ? t('accountManagement.ui.errorWithNotice', { error: accountDisplayMessage(t, e?.message) || t('accountManagement.ui.operationFailed'), notice: t(quotaMessageKey) })
        : accountDisplayMessage(t, e?.message) || t('accountManagement.ui.operationFailed')
    }
  } finally { busy.value = false }
}

const runWithPassword = (task: (password: string) => Promise<void>) => run(async () => {
  await withWalletPassword(task)
})

const runPreflight = () => runWithPassword(async password => {
	preflight.value = await accountSDK.preflight(password, metadata())
  storageOptions.value = (await accountSDK.getStorageOptions()).options
  recordCount.value = storageOptions.value.find(option => option.id === 'paid')?.default_record_count || 100
  step.value = 2
})

const paidStorageOption = computed(() => storageOptions.value.find(option => option.id === 'paid'))
const normalizedRecordCount = computed(() => Number(recordCount.value))
const recordCountValid = computed(() => selectedStorage.value !== 'paid'
  || (Number.isSafeInteger(normalizedRecordCount.value) && normalizedRecordCount.value >= 100))
const quoteForRecordCount = (count: number) => {
  const fee = Number(paidStorageOption.value?.full_record_fee_per_block)
  if (!Number.isSafeInteger(count) || count < 100 || !Number.isFinite(fee) || fee <= 0) return null
  const minimum = Number(paidStorageOption.value?.minimum_amount_per_block || 0)
  const quoted = count * fee
  const amountPerBlock = Math.max(quoted, minimum)
  const format = (value: number) => value.toLocaleString(undefined, { maximumFractionDigits: 8 })
  return {
    amountPerBlock: format(amountPerBlock),
    initialCost: format(amountPerBlock * 1000),
    annualCost: format(amountPerBlock * 2_628_000),
  }
}
const autopayQuote = computed(() => quoteForRecordCount(normalizedRecordCount.value))

const paidConfirmRows = computed(() => {
  const option = paidStorageOption.value
  const confirmationCount = paidConfirmContext.value === 'guardian' ? 100 : normalizedRecordCount.value
  const quote = quoteForRecordCount(confirmationCount)
  const funding = paidConfirmContext.value === 'funding'
  const fundingQuote = fundingConfirmation.value
  // AUTOPAY funding uses the account-management root at account zero, not
  // necessarily the wallet/sub-account currently selected in the wallet UI.
  const rootWalletId = savedState.value?.root_wallet_id
  const rootWallet = wallets.value.find(wallet => String(wallet.id) === String(rootWalletId))
  return [
    {
      label: t('tools.txConfirm.purpose'),
      value: paidConfirmContext.value === 'guardian'
        ? t('accountManagement.ui.guardianPaymentPurpose')
        : funding
          ? t('accountManagement.dkvsPay')
        : t('accountManagement.autopayPurpose'),
    },
    ...[
      {
        label: t('accountManagement.fundingSourceWallet'),
        value: rootWallet ? `${rootWallet.name} (${rootWallet.id})` : String(rootWalletId ?? ''),
      },
      { label: t('tools.txConfirm.account'), value: t('accountManagement.fundingSourceAccount') },
      { label: t('accountManagement.ui.rootIdentity'), value: preflight.value?.account_id || savedState.value?.account_id || walletStore.rootAccountId },
      { label: t('tools.txConfirm.sourceAddress'), value: funding ? fundingQuote?.payer || '' : '' },
    ],
    { label: t('tools.txConfirm.to'), value: funding ? fundingQuote?.contract_address || '' : option?.contract_address || '' },
    { label: t('tools.txConfirm.asset'), value: funding ? fundingQuote?.fee_asset || '' : option?.fee_asset || '' },
    { label: t(funding ? 'accountManagement.fundingPrincipal' : 'tools.txConfirm.amount'), value: funding ? fundingQuote?.recommended_funding_amount || '' : quote?.initialCost || option?.estimated_cost || '' },
    { label: t('tools.txConfirm.network'), value: `SatoshiNet ${walletStore.network}` },
    { label: t('accountManagement.recordCount'), value: funding ? '' : String(confirmationCount) },
    {
      label: t('accountManagement.perBlock'),
      value: funding
        ? `${fundingQuote?.effective_amount_per_block || ''} ${fundingQuote?.fee_asset || ''}`.trim()
        : quote ? `${quote.amountPerBlock} ${option?.fee_asset || ''}` : '',
    },
    {
      label: t('accountManagement.fundingPeriod'),
      value: t('accountManagement.fundingBlocks', {
        count: funding ? fundingQuote?.recommended_funding_blocks || 1000 : 1000,
      }),
    },
    ...(funding && fundingQuote?.estimated_funded_expiry_height ? [{
      label: t('accountManagement.dkvsFundedExpiry'),
      value: t('accountManagement.dkvsExpiryEstimate', { height: fundingQuote.estimated_funded_expiry_height }),
    }] : []),
    ...(funding ? [
      { label: t('tools.txConfirm.operatingGas'), value: t('accountManagement.fundingOperatingGas') },
    ] : []),
  ].filter(row => row.value)
})

const resolvePaidConfirmation = (confirmed: boolean) => {
  const resolver = paidConfirmResolver
  paidConfirmResolver = null
  paidConfirmOpen.value = false
  if (resolver) resolver(confirmed)
}

const handlePaidConfirmOpenChange = (open: boolean) => {
  if (!open) return resolvePaidConfirmation(false)
  paidConfirmOpen.value = true
}

const requestPaidConfirmation = () => new Promise<boolean>((resolve) => {
  if (paidConfirmResolver) paidConfirmResolver(false)
  paidConfirmResolver = resolve
  paidConfirmOpen.value = true
})

const selectStorage = (option: AccountStorageOption) => {
  selectedStorage.value = option.id
  if (option.id === 'paid' && (!Number.isSafeInteger(normalizedRecordCount.value) || normalizedRecordCount.value < 100)) {
    recordCount.value = option.default_record_count || 100
  }
}

const confirmStorage = () => run(async () => {
  if (!recordCountValid.value) throw new Error(t('accountManagement.ui.invalidRecordCount'))
  paidConfirmContext.value = 'account'
  if (selectedStorage.value === 'paid' && !await requestPaidConfirmation()) return
  await accountSDK.confirmStorage(
    selectedStorage.value,
    selectedStorage.value === 'paid' ? normalizedRecordCount.value : undefined,
  )
  step.value = 3
})

const reusePaidStorage = () => run(async () => {
  if (!recordCountValid.value) throw new Error(t('accountManagement.ui.invalidRecordCount'))
  await accountSDK.reusePaidStorage(normalizedRecordCount.value)
  selectedStorage.value = 'paid'
  step.value = 3
})

const cancelStorageSetup = () => run(async () => {
  await accountSDK.cancelPendingStorageAuthorization()
  questions.value.forEach(question => {
    question.answer = ''
    question.confirmation = ''
  })
  step.value = 2
})

const cancelReconfiguration = () => run(async () => {
  await accountSDK.cancelPendingStorageAuthorization()
  questions.value.forEach(question => { question.answer = ''; question.confirmation = '' })
  guardianContact.value = ''
  selectedStorage.value = ''
  preflight.value = null
  step.value = 1
  reconfiguring.value = false
})

const refreshAutopayStatus = async () => {
  const status = await accountSDK.autopayStatus()
  if (!disposed) autopayStatus.value = status
}

const refreshAccountStatus = () => accountManagementState.refresh()

const retryDKVSSync = () => run(async () => {
  await accountManagementState.retryNow()
})

const loadDKVSInspection = async (cursor?: string) => {
  if (viewerLoading.value) return
  viewerLoading.value = true
  viewerError.value = ''
  try {
    const result = await accountSDK.inspectDKVS(cursor)
    if (disposed) return
    viewer.value = result
    selectedViewerKey.value = result.records[0]?.key || ''
  } catch (e: any) {
    if (!disposed) {
      viewer.value = null
      viewerError.value = accountDisplayMessage(t, e?.message) || t('accountManagement.ui.cacheReadFailed')
    }
  } finally {
    viewerLoading.value = false
  }
}

const refreshVisibleStatus = async () => {
  if (disposed || document.visibilityState !== 'visible') return
  try { await refreshAutopayStatus() } catch { /* preserve the last known status */ }
}
const onVisibilityChange = () => { void refreshVisibleStatus() }

const fundAutopay = () => run(async () => {
  await refreshAutopayStatus()
  if (!autopayStatus.value?.can_fund) throw new Error(t('accountManagement.ui.fundingUnavailable'))
  const confirmed = { ...autopayStatus.value }
  fundingConfirmation.value = confirmed
  paidConfirmContext.value = 'funding'
  try {
    if (!await requestPaidConfirmation()) return
    autopayFundingResult.value = await accountSDK.fundAutopay(confirmed)
  } finally {
    fundingConfirmation.value = null
    try { await refreshAutopayStatus() } catch { /* keep the receipt or submission error */ }
  }
})

const resumeAutopay = () => run(async () => {
  await refreshAutopayStatus()
  if (!autopayStatus.value?.funding_pending || !autopayStatus.value.funding_can_resume) return
  try {
    autopayFundingResult.value = await accountSDK.fundAutopay({ ...autopayStatus.value })
  } finally {
    try { await refreshAutopayStatus() } catch { /* keep the original receipt */ }
  }
})

const createRecovery = () => runWithPassword(async password => {
  for (const q of questions.value) {
    if (!q.prompt.trim() || q.answer.length < 8 || q.answer !== q.confirmation) throw new Error(t('accountManagement.ui.invalidAnswers'))
  }
  let guardian: any = undefined
  if (recoveryMode.value === '2of3') {
    if (guardianContactError.value || !parsedGuardianContact.value) throw new Error(guardianContactError.value)
    guardian = parsedGuardianContact.value
  }
	const result = await accountSDK.createRecovery({
	password,
    wallets: metadata(),
    recovery_mode: recoveryMode.value,
    questions: questions.value,
    guardian,
  })
  if (disposed) {
    await accountSDK.abortSession(result.session_id)
    return
  }
  creation.value = result
  guardianSetupVerified.value = false
  guardianReceipt.value = ''
  rehearsalLocator.value = ''
  questions.value.forEach(q => { q.answer = ''; q.confirmation = '' })
  step.value = 4
})

const verifyGuardian = () => run(async () => {
  const result = await accountSDK.checkGuardianSetup(creation.value.session_id, guardianReceipt.value)
  creation.value.locator = result.locator
  guardianSetupVerified.value = true
})

const verifySavedLocator = () => {
  if (recoveryMode.value === '2of3' && (!guardianSetupVerified.value || rehearsalLocator.value.trim() !== creation.value?.locator)) {
    throw new Error(t('accountManagement.ui.finalLocatorRequired'))
  }
}

const createRehearsalGuardianRequest = () => run(async () => {
  verifySavedLocator()
  rehearsalGuardianRequest.value = (await accountSDK.createGuardianRequest(creation.value.session_id)).request
  rehearsalGuardianResponse.value = ''
  rehearsalGuardianReady.value = false
})

const acceptRehearsalGuardianResponse = () => run(async () => {
  await accountSDK.consumeGuardianResponse(creation.value.session_id, rehearsalGuardianResponse.value.trim())
  rehearsalGuardianReady.value = true
})

const rehearse = () => runWithPassword(async password => {
  verifySavedLocator()
  const answers = rehearsalAnswers.value.map((answer, index) => ({ question_id: questions.value[index].id, answer })).filter(item => item.answer)
	const result = await accountSDK.rehearse(
	creation.value.session_id, answers, rehearsalUserShare.value, password
	)
  if (!result.verified) throw new Error(t('accountManagement.ui.rehearsalFailed'))
  rehearsalAnswers.value = ['', '', '']
  rehearsalUserShare.value = ''
  rehearsalLocator.value = ''
  rehearsalGuardianRequest.value = ''
  rehearsalGuardianResponse.value = ''
  rehearsalGuardianReady.value = false
  reconfiguring.value = false
  step.value = 6
  await refreshAccountStatus()
})

const generateGuardianIdentity = () => runWithPassword(async password => {
	guardianIdentity.value = (await accountSDK.guardianIdentity(password)).contact
  guardianIdentityCopied.value = false
})

const copyGuardianIdentity = () => run(async () => {
  await copyText(guardianIdentity.value)
  guardianIdentityCopied.value = true
})

const acceptGuardianSetup = () => runWithPassword(async password => {
  paidConfirmContext.value = 'guardian'
  if (guardianStorageChoice.value === 'paid' && !await requestPaidConfirmation()) return
  await accountSDK.confirmStorage(guardianStorageChoice.value, guardianStorageChoice.value === 'paid' ? 100 : undefined)
	guardianReceiptOutput.value = (await accountSDK.acceptGuardianSetup(password, guardianSetupInput.value)).receipt
})

const createGuardianResponse = () => runWithPassword(async password => {
	guardianResponse.value = (await accountSDK.createGuardianResponse(password, guardianRecoveryRequest.value)).response
})

const copyText = async (value: string) => {
  if (value) await navigator.clipboard.writeText(value)
}

onMounted(async () => {
  await refreshAccountStatus()
  try { storageOptions.value = (await accountSDK.getStorageOptions()).options } catch { /* preflight will retry */ }
  if (!savedState.value?.recovery_configured) {
    try {
      const resumed = await accountSDK.resumePendingStorageAuthorization()
      if (resumed?.id) {
        selectedStorage.value = resumed.mode || resumed.summary?.mode || ''
        if (resumed.summary?.record_count) recordCount.value = Number(resumed.summary.record_count)
        step.value = 3
      }
    } catch { /* expired or identity-mismatched authorization is intentionally ignored */ }
  }
  try { await refreshAutopayStatus() } catch { autopayStatus.value = null }
  if (disposed) return
  autopayRefreshTimer = setInterval(() => {
    if (document.visibilityState === 'visible') void refreshAutopayStatus().catch(() => undefined)
  }, 30_000)
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onBeforeUnmount(() => {
  disposed = true
  viewer.value = null
  if (autopayRefreshTimer) clearInterval(autopayRefreshTimer)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  if (paidConfirmResolver) paidConfirmResolver(false)
  if (creation.value?.session_id) void accountSDK.abortSession(creation.value.session_id).catch(() => undefined)
  questions.value.forEach(question => { question.answer = ''; question.confirmation = '' })
  creation.value = null
  rehearsalAnswers.value = []
  rehearsalUserShare.value = guardianResponse.value = ''
})
</script>
