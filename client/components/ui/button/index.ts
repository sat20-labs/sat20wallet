import { cva, type VariantProps } from 'class-variance-authority'

export { default as Button } from './Button.vue'

export const buttonVariants = cva(
  'inline-flex items-center justify-center gap-1 whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        default: 'bg-gradient-to-r from-[#6600cc] to-[#a0076d] text-white shadow hover:opacity-90',  // 修改按钮为渐变底色
        destructive:
          'bg-destructive text-destructive-foreground shadow-sm hover:bg-destructive/90',
        outline:
          'border border-input/80 bg-accent/60 shadow-sm hover:bg-accent/90 text-accent-foreground/70 hover:text-accent-foreground',
        secondary:
          'border border-primary/60 bg-muted/10 text-secondary-foreground shadow-sm hover:bg-muted/50',
        ghost: 'hover:bg-accent hover:text-accent-foreground',
        link: 'bg-transparent text-foreground/40 underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-9 px-4 py-2',
        xs: 'h-7 rounded px-1',
        sm: 'h-8 rounded-md px-2 text-xs',
        lg: 'h-10 rounded-md px-8',
        icon: 'h-9 w-9',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

export type ButtonVariants = VariantProps<typeof buttonVariants>
