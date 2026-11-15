import { createContext } from 'react';
import type { BrowserSession } from './client.js';

export const BrowserSessionChanged = createContext<(session: BrowserSession) => void>(() => {});
