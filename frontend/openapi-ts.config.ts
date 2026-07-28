import { defineConfig } from '@hey-api/openapi-ts'

export default defineConfig({
  input: '../backend/api/openapi.yaml',
  output: {
    path: 'src/generated/sdk',
  },
  plugins: [
    '@hey-api/typescript',
    {
      name: '@hey-api/client-fetch',
      runtimeConfigPath: './src/api-client-config',
    },
    '@hey-api/sdk',
  ],
})
