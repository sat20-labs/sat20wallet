<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button
        variant="outline"
        size="sm"
        class="w-full h-8 flex justify-between items-center"
      >
        <span v-if="selectedValues.length">{{ showText }}</span>
        <span class="text-muted-foreground" v-else>{{placeholder}}</span>
        <CaretSortIcon class="w-4 h-4 text-muted-foreground" />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent>
      <DropdownMenuCheckboxItem
        v-for="column in options"
        :key="column.value"
        class="capitalize"
        :checked="selectedValues.includes(column.value)"
        @update:checked="(value: any) => updateSelectedValues(column.value, value)"
      >
        {{ column.label }}
      </DropdownMenuCheckboxItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>

<script setup lang='ts'>
import { CaretSortIcon } from '@radix-icons/vue'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
const props = defineProps<{
  options: { label: string; value: any }[]
  placeholder?: string
}>()

const selectedValues = defineModel<string[]>({
  default: () => [],
  required: false,
})

const showText = computed(() => {
  const firstSelectedOption = props.options.find((option) => option.value === selectedValues.value[0])
  if (selectedValues.value.length === 1) {
    return firstSelectedOption?.label
  } else {
    return `${firstSelectedOption?.label} (+${selectedValues.value.length - 1})`
  }
})
const updateSelectedValues = (value: string, checked: boolean) => {
  if (checked) {
    selectedValues.value = [...selectedValues.value, value].sort((a, b) => {
      return props.options.findIndex(option => option.value === a) - props.options.findIndex(option => option.value === b)
    })
  } else {
    selectedValues.value = selectedValues.value.filter((v) => v !== value)
  }
}

</script>

