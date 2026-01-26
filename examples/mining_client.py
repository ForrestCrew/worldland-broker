#!/usr/bin/env python3
"""
Worldland Mining Client

이 스크립트는 Mining Container 내부에서 실행되어
k8s-proxy-server의 Mining API를 호출합니다.

사용 예:
    # GPU 추가 할당
    python mining_client.py allocate --gpu-count 2 --reason "difficulty_spike"
    
    # GPU 반환
    python mining_client.py release --gpu-count 1
    
    # 상태 확인
    python mining_client.py status
"""

import os
import sys
import json
import argparse
import requests
from typing import Optional

# 환경 변수에서 설정 읽기
K8S_PROXY_URL = os.getenv("K8S_PROXY_URL", "http://k8s-proxy-svc.default:8080")
PROVIDER_ID = os.getenv("PROVIDER_ID", "")


class MiningClient:
    """Mining API 클라이언트"""
    
    def __init__(self, base_url: str, provider_id: str):
        self.base_url = base_url.rstrip("/")
        self.provider_id = provider_id
        
    def _url(self, path: str) -> str:
        return f"{self.base_url}/api/v1/providers/{self.provider_id}/mining{path}"
    
    def get_status(self) -> dict:
        """현재 채굴 상태 조회"""
        response = requests.get(self._url(""))
        response.raise_for_status()
        return response.json()
    
    def allocate_gpu(self, gpu_count: int, gpu_type: str = "", reason: str = "") -> dict:
        """채굴용 GPU 추가 할당"""
        payload = {"gpu_count": gpu_count}
        if gpu_type:
            payload["gpu_type"] = gpu_type
        if reason:
            payload["reason"] = reason
            
        response = requests.post(
            self._url("/allocate"),
            json=payload,
            headers={"Content-Type": "application/json"}
        )
        return response.json()
    
    def release_gpu(self, gpu_count: int, gpu_type: str = "") -> dict:
        """채굴용 GPU 반환"""
        payload = {"gpu_count": gpu_count}
        if gpu_type:
            payload["gpu_type"] = gpu_type
            
        response = requests.post(
            self._url("/release"),
            json=payload,
            headers={"Content-Type": "application/json"}
        )
        return response.json()
    
    def stop_mining(self) -> dict:
        """채굴 중지"""
        response = requests.post(self._url("/stop"))
        return response.json()


def main():
    parser = argparse.ArgumentParser(description="Worldland Mining Client")
    parser.add_argument("--url", default=K8S_PROXY_URL, help="k8s-proxy-server URL")
    parser.add_argument("--provider-id", default=PROVIDER_ID, help="Provider ID")
    
    subparsers = parser.add_subparsers(dest="command", required=True)
    
    # status 명령
    subparsers.add_parser("status", help="현재 채굴 상태 조회")
    
    # allocate 명령
    allocate_parser = subparsers.add_parser("allocate", help="채굴용 GPU 추가 할당")
    allocate_parser.add_argument("--gpu-count", type=int, required=True, help="할당할 GPU 수")
    allocate_parser.add_argument("--gpu-type", default="", help="GPU 타입")
    allocate_parser.add_argument("--reason", default="", help="할당 이유")
    
    # release 명령
    release_parser = subparsers.add_parser("release", help="채굴용 GPU 반환")
    release_parser.add_argument("--gpu-count", type=int, required=True, help="반환할 GPU 수")
    release_parser.add_argument("--gpu-type", default="", help="GPU 타입")
    
    # stop 명령
    subparsers.add_parser("stop", help="채굴 중지")
    
    args = parser.parse_args()
    
    if not args.provider_id:
        print("Error: Provider ID가 필요합니다. --provider-id 또는 PROVIDER_ID 환경변수를 설정하세요.")
        sys.exit(1)
    
    client = MiningClient(args.url, args.provider_id)
    
    try:
        if args.command == "status":
            result = client.get_status()
            print(json.dumps(result, indent=2))
            
        elif args.command == "allocate":
            result = client.allocate_gpu(
                gpu_count=args.gpu_count,
                gpu_type=args.gpu_type,
                reason=args.reason
            )
            if result.get("success"):
                print(f"✅ GPU {args.gpu_count}개 할당 성공!")
            else:
                print(f"❌ 할당 실패: {result.get('message')}")
            print(json.dumps(result, indent=2))
            
        elif args.command == "release":
            result = client.release_gpu(
                gpu_count=args.gpu_count,
                gpu_type=args.gpu_type
            )
            if result.get("success"):
                print(f"✅ GPU {args.gpu_count}개 반환 성공!")
            else:
                print(f"❌ 반환 실패: {result.get('error')}")
            print(json.dumps(result, indent=2))
            
        elif args.command == "stop":
            result = client.stop_mining()
            print(json.dumps(result, indent=2))
            
    except requests.exceptions.RequestException as e:
        print(f"❌ API 요청 실패: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
