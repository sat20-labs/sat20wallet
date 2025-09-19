<script setup lang="ts">
import { Select, SelectTrigger, SelectValue, SelectContent, SelectGroup, SelectItem } from '@/components/ui/select'
interface Option {
  label: string;
  value: any;
}

const props = withDefaults(
  defineProps<{
    options: Option[];
    placeholder?: string;
    disabled?: boolean;
  }>(),
  {
    options: () => [],
    placeholder: 'Select',
    disabled: false,
  },
);

const { options } = toRefs(props);
const selectedValue = defineModel<any>({
  required: false,
});

const updateValue = (e: any) => {
  console.log('updateValue', e)
}
</script>

<template>
  <Select v-model="selectedValue" :disabled="props.disabled" @update:model-value="updateValue">
    <SelectTrigger class="w-full">
      <SelectValue :placeholder="props.placeholder" />
    </SelectTrigger>
    <SelectContent>
      <SelectGroup>
        <SelectItem
          v-for="option in options"
          :key="option.value"
          :value="option.value"
        >
          {{ option.label }}
        </SelectItem>
      </SelectGroup>
    </SelectContent>
  </Select>
</template>
