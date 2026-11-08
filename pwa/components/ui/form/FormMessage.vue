<script lang="ts" setup>
import { useI18n } from 'vue-i18n'
import { ErrorMessage } from 'vee-validate'
import { toValue } from 'vue'
import { useFormField } from './useFormField'

const { t, te } = useI18n()
const { name, formMessageId } = useFormField()
</script>

<template>
  <ErrorMessage
    :id="formMessageId"
    as="p"
    :name="toValue(name)"
    class="text-[0.8rem] font-medium text-destructive"
    v-slot="{ message }"
  >
    {{ message && te(message) ? t(message) : message }}
  </ErrorMessage>
</template>
