import fs from 'fs';
import path from 'path';

const pluginDistPath = path.resolve(__dirname, '../../dist');

export function hasAimockPluginDist(): boolean {
	return fs.existsSync(pluginDistPath) && fs.readdirSync(pluginDistPath).some((file) => file.endsWith('.tar.gz'));
}
