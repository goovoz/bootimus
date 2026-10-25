export interface StaticArtifact {
  kind: 'container' | 'appliance';
  platform: string;
  label: string;
  url: string;
  filename?: string;
  size?: string;
  notes?: string;
}

export interface Tool {
  id: string;
  name: string;
  summary: string;
  upstream: string;
  mirror: string;
  version: string;
  size: string;
}

export const dockerImage: StaticArtifact = {
  kind: 'container',
  platform: 'docker',
  label: 'Docker image (multi-arch)',
  url: 'https://hub.docker.com/r/garybowers/bootimus',
  notes: 'amd64 + arm64 · pull garybowers/bootimus:latest',
};

export const applianceImage: StaticArtifact = {
  kind: 'appliance',
  platform: 'usb',
  label: 'USB Appliance (Alpine + bootimus)',
  filename: 'bootimus-appliance-latest.img.xz',
  url: 'https://dl.bootimus.com/appliance/bootimus-appliance-latest.img.xz',
  size: '~2 GiB',
  notes: 'Flash with dd or balenaEtcher. Boots straight into bootimus.',
};

export const tools: Tool[] = [
  {
    id: 'gparted',
    name: 'GParted Live',
    summary: 'Partition editor. Resize, move, copy, create, delete partitions on a running system.',
    upstream: 'https://gparted.org/download.php',
    mirror: 'https://dl.bootimus.com/tools/gparted/gparted-live-1.6.0-3-amd64.iso',
    version: '1.6.0-3',
    size: '499 MiB',
  },
  {
    id: 'clonezilla',
    name: 'Clonezilla Live',
    summary: 'Disk cloning and imaging. Bare-metal backup and restore over the network.',
    upstream: 'https://clonezilla.org/',
    mirror: 'https://dl.bootimus.com/tools/clonezilla/clonezilla-live-3.2.0-5-amd64.iso',
    version: '3.2.0-5',
    size: '395 MiB',
  },
  {
    id: 'memtest',
    name: 'Memtest86+',
    summary: 'RAM diagnostics. Pre-boot memory test, UEFI + BIOS.',
    upstream: 'https://memtest.org/',
    mirror: 'https://dl.bootimus.com/tools/memtest/memtest86plus-7.00.iso',
    version: '7.00',
    size: '2 MiB',
  },
  {
    id: 'systemrescue',
    name: 'SystemRescue',
    summary: 'Full rescue toolkit. File recovery, disk repair, network diagnostics, partition tools.',
    upstream: 'https://www.system-rescue.org/',
    mirror: 'https://dl.bootimus.com/tools/systemrescue/systemrescue-11.00-amd64.iso',
    version: '11.00',
    size: '853 MiB',
  },
  {
    id: 'shredos',
    name: 'ShredOS',
    summary: 'Secure disk wiping. nwipe-based. DoD 5220.22-M, Gutmann, custom passes.',
    upstream: 'https://github.com/PartialVolume/shredos.x86_64',
    mirror: 'https://dl.bootimus.com/tools/shredos/shredos-2024.12.iso',
    version: '2024.12',
    size: '28 MiB',
  },
  {
    id: 'netbootxyz',
    name: 'netboot.xyz',
    summary: 'Chainloads into hundreds of OS installers, live distros, and utilities.',
    upstream: 'https://netboot.xyz/',
    mirror: 'https://dl.bootimus.com/tools/netbootxyz/netboot.xyz.efi',
    version: 'rolling',
    size: '1 MiB',
  },
];
