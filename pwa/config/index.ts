const devConfig = {
  ordxBaseUrl: 'https://apidev.sat20.org',
  satnetBaseUrl: 'https://apidev.sat20.org',
}

const testConfig = {
  ordxBaseUrl: 'https://apitest.ordx.market',
  satnetBaseUrl: 'https://apitest.ordx.market',
}

const prodConfig = {
  ordxBaseUrl: 'https://apiprd.ordx.market',
  satnetBaseUrl: 'https://apiprd.ordx.market',
}

const regtestConfig = {
  ordxBaseUrl: 'http://127.0.0.1:30143',
  satnetBaseUrl: 'http://127.0.0.1:30262',
}


export const config: Record<string, any> = {
  dev: devConfig,
  test: testConfig,
  prd: prodConfig,
  regtest: regtestConfig,
}
