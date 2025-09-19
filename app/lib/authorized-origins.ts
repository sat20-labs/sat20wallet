import { Storage } from '@capacitor/storage';

const AUTHORIZED_ORIGINS_KEY = 'local:authorized_origins';

// 获取授权列表
export const getAuthorizedOrigins = async (): Promise<string[]> => {
  const { value } = await Storage.get({ key: AUTHORIZED_ORIGINS_KEY });
  return value ? JSON.parse(value) : [];
};

// 添加授权来源
export const addAuthorizedOrigin = async (origin: string): Promise<void> => {
  const origins = await getAuthorizedOrigins();
  if (!origins.includes(origin)) {
    origins.push(origin);
    await Storage.set({ key: AUTHORIZED_ORIGINS_KEY, value: JSON.stringify(origins) });
  }
};

// 验证来源是否授权
export const isOriginAuthorized = async (origin: string): Promise<boolean> => {
  const origins = await getAuthorizedOrigins();
  return origins.includes(origin);
}; 