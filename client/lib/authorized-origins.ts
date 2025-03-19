import { storage } from 'wxt/storage';

const AUTHORIZED_ORIGINS_KEY = 'local:authorized_origins';

// 获取授权列表
export const getAuthorizedOrigins = async (): Promise<string[]> => {
  const origins = await storage.getItem<string[]>(AUTHORIZED_ORIGINS_KEY);
  return origins || [];
};

// 添加授权来源
export const addAuthorizedOrigin = async (origin: string): Promise<void> => {
  const origins = await getAuthorizedOrigins();
  if (!origins.includes(origin)) {
    origins.push(origin);
    await storage.setItem(AUTHORIZED_ORIGINS_KEY, origins);
  }
};

// 验证来源是否授权
export const isOriginAuthorized = async (origin: string): Promise<boolean> => {
  const origins = await getAuthorizedOrigins();
  return origins.includes(origin);
}; 