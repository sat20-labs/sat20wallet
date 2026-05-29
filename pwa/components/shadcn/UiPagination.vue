<template>
  <Pagination
    v-slot="{ page }"
    :total="total"
    :sibling-count="siblingCount"
    :show-edges="showEdges"
    :default-page="defaultPage"
    :disabled="disabled"
    :items-per-page="itemsPerPage"
    :page="page"
    @update:page="handlePageChange"
  >
    <PaginationList v-slot="{ items }" class="flex items-center gap-1">
      <PaginationFirst />
      <PaginationPrev />

      <template v-for="(item, index) in items">
        <PaginationListItem
          v-if="item.type === 'page'"
          :key="index"
          :value="item.value"
          as-child
        >
          <Button
            class="w-10 h-10 p-0"
            :variant="item.value === page ? 'default' : 'outline'"
          >
            {{ item.value }}
          </Button>
        </PaginationListItem>
        <PaginationEllipsis v-else :key="item.type" :index="index" />
      </template>

      <PaginationNext />
      <PaginationLast />
    </PaginationList>
  </Pagination>
</template>
<script setup lang="ts">
import { Pagination, PaginationList, PaginationListItem, PaginationFirst, PaginationPrev, PaginationNext, PaginationLast, PaginationEllipsis } from '@/components/ui/pagination'

const props = defineProps({
  defaultPage: {
    type: Number,
    default: 1,
  },
  disabled: {
    type: Boolean,
    default: false,
  },
  itemsPerPage: {
    type: Number,
    default: 10,
  },
  page: {
    type: Number,
    default: 1,
  },
  showEdges: {
    type: Boolean,
    default: false,
  },
  siblingCount: {
    type: Number,
    default: 2,
  },
  total: {
    type: Number,
    default: 0,
  },
});

const emits = defineEmits(['update:page']);

function handlePageChange(newPage: number) {
  emits('update:page', newPage);
}
</script>
