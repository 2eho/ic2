#!/usr/bin/env node
/** 桥接器入口。 */
import { loadConfig } from './config.js';
import { startBridge } from './server.js';

const cfg = loadConfig();
startBridge(cfg, console);
