import { toast } from 'vue-sonner'
import { CheckCircle, XCircle, AlertCircle, Info } from 'lucide-vue-next'
import type { Component } from 'vue'

export interface ToastProps {
  title?: string
  description?: string
  variant?: 'default' | 'destructive' | 'success' | 'info'
  duration?: number
  icon?: Component
}

export function useToast() {
  const showToast = (props: ToastProps) => {
    const { title, description, variant = 'default', duration = 3000, icon } = props
    
    const toastOptions = {
      description,
      duration,
      icon: icon || getDefaultIcon(variant)
    }

    switch (variant) {
      case 'success':
        return toast.success(title || 'Success', toastOptions)
      case 'destructive':
        return toast.error(title || 'Error', toastOptions)
      case 'info':
        return toast.info(title || 'Info', toastOptions)
      default:
        return toast(title || 'Notification', toastOptions)
    }
  }

  return {
    toast: showToast
  }
}

function getDefaultIcon(variant: string): Component | undefined {
  switch (variant) {
    case 'success':
      return CheckCircle
    case 'destructive':
      return XCircle
    case 'info':
      return Info
    default:
      return AlertCircle
  }
}