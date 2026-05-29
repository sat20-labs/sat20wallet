const devConfig = {
  ordxBaseUrl: 'https://apidev.sat20.org',
  satnetBaseUrl: 'https://apidev.sat20.org',
}

const testConfig = {
  ordxBaseUrl: 'https://apitest.sat20.org',
  satnetBaseUrl: 'https://apitest.sat20.org',
}

const prodConfig = {
  ordxBaseUrl: 'https://apiprd.ordx.market',
  satnetBaseUrl: 'https://apiprd.ordx.market',
}


export const config: Record<string, any> = {
  dev: devConfig,
  test: testConfig,
  prd: prodConfig,
}