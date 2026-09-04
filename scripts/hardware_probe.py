import json
import os
import platform
import shutil
import subprocess
from pathlib import Path


def read_mem_gb() -> float:
    path = Path('/proc/meminfo')
    if not path.exists():
        return 0.0
    values = {}
    for line in path.read_text().splitlines():
        key, value = line.split(':', 1)
        values[key] = value.strip()
    kb = int(values.get('MemTotal', '0 kB').split()[0])
    return round(kb / 1024 / 1024, 2)


def cpu_info() -> tuple[str, set[str], int]:
    model = platform.processor() or 'unknown'
    flags = set()
    logical = os.cpu_count() or 0
    path = Path('/proc/cpuinfo')
    if path.exists():
        for line in path.read_text(errors='ignore').splitlines():
            if line.startswith('model name') and model == 'unknown':
                model = line.split(':', 1)[1].strip()
            if line.startswith('flags'):
                flags.update(line.split(':', 1)[1].split())
                break
    return model, flags, logical


def lscpu_field(name: str) -> str:
    if not shutil.which('lscpu'):
        return ''
    result = subprocess.run(['lscpu'], capture_output=True, text=True, check=False)
    for line in result.stdout.splitlines():
        if line.lower().startswith(name.lower() + ':'):
            return line.split(':', 1)[1].strip()
    return ''


def main():
    model, flags, logical = cpu_info()
    avx = 'avx' in flags
    avx2 = 'avx2' in flags
    avx512 = 'avx512f' in flags
    report = {
        'cpu_model': model,
        'architecture': platform.machine(),
        'logical_cpus': logical,
        'sockets': lscpu_field('Socket(s)'),
        'cores_per_socket': lscpu_field('Core(s) per socket'),
        'numa_nodes': lscpu_field('NUMA node(s)'),
        'memory_gb': read_mem_gb(),
        'avx': avx,
        'avx2': avx2,
        'avx512f': avx512,
        'vllm_cpu_ready': avx2,
        'legacy_cpu_ready': avx,
        'vllm_cpu_tier': 'recommended' if avx512 else 'limited' if avx2 else 'unsupported',
    }
    print(json.dumps(report, indent=2))
    if not avx:
        raise SystemExit('no supported x86 CPU profile: AVX is required for the legacy runtime and AVX2 for vLLM CPU')


if __name__ == '__main__':
    main()
