/**
 * Type Conversion Utilities
 * Handles conversion of URL query parameters from strings to appropriate types
 */

import type { LocationQueryValue } from 'vue-router'

/**
 * Converts a query parameter value to a number
 * @param value - The query parameter value (string, string array, LocationQueryValue, or undefined)
 * @returns The converted number or null if conversion fails
 */
export function convertQueryToNumber(value: string | string[] | LocationQueryValue | LocationQueryValue[] | undefined): number | null {
  if (value === undefined || value === null) {
    return null
  }

  // Handle string arrays - take the first element
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return null
    }
    value = value[0]
  }

  // Convert string to number
  const parsedNumber = Number(value)
  if (isNaN(parsedNumber)) {
    return null
  }

  return parsedNumber
}

/**
 * Converts a query parameter value to a string
 * @param value - The query parameter value (string, string array, LocationQueryValue, or undefined)
 * @returns The string value or null if invalid
 */
export function convertQueryToString(value: string | string[] | LocationQueryValue | LocationQueryValue[] | undefined): string | null {
  if (value === undefined || value === null) {
    return null
  }

  // Handle string arrays - take the first element
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return null
    }
    value = value[0]
  }

  // Ensure it's a string
  return String(value)
}

/**
 * Converts a query parameter value to a boolean
 * @param value - The query parameter value (string, string array, or undefined)
 * @returns The boolean value or null if invalid
 */
export function convertQueryToBoolean(value: string | string[] | undefined): boolean | null {
  if (value === undefined || value === null) {
    return null
  }

  // Handle string arrays - take the first element
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return null
    }
    value = value[0]
  }

  // Convert to string and check for truthy values
  const stringValue = String(value).toLowerCase()
  return stringValue === 'true' || stringValue === '1' || stringValue === 'yes'
}