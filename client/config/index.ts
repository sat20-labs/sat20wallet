const devConfig = {
  ordxBaseUrl: 'https://apidev.sat20.org',
  satnetBaseUrl: 'https://apidev.sat20.org',
}

const testConfig = {
  ordxBaseUrl: 'https://apitest.sat20.org',
  satnetBaseUrl: 'https://apitest.sat20.org',
}

const prodConfig = {
  ordxBaseUrl: 'https://apiprd.sat20.org',
  satnetBaseUrl: 'https://apiprd.sat20.org',
}


export const config: Record<string, any> = {
  dev: devConfig,
  test: testConfig,
  prod: prodConfig,
}