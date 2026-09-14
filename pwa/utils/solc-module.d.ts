declare module 'virtual:sat20-soljson' {
  const module: {
    cwrap(name: string, returnType: string, argumentTypes: string[]): (...args: unknown[]) => string
  }
  export default module
}
