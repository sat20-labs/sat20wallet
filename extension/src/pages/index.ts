import React from 'react';
import { Button } from 'shadcn/ui';

export default function Index() {
  return (
    <div>
      <Button onClick={() => console.log('Create Wallet clicked')}>创建钱包</Button>
      <Button onClick={() => console.log('Import Wallet clicked')}>导入钱包</Button>
    </div>
  );
}