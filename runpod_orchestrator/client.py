import aiohttp
from typing import Optional, Dict, Any, List
from .config import CLERK_SESSION_ID, CLERK_COOKIE, RUNPOD_GRAPHQL_URL, RUNPOD_HAPI_HOST


class RunpodClient:
    def __init__(self):
        self.session: Optional[aiohttp.ClientSession] = None

    async def __aenter__(self):
        self.session = aiohttp.ClientSession()
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self.session:
            await self.session.close()

    async def get_jwt(self) -> str:
        """Retrieves temporary JWT using session cookies."""
        url = f"https://clerk.runpod.io/v1/client/sessions/{CLERK_SESSION_ID}/tokens"
        async with self.session.post(
            url, cookies={"__client": CLERK_COOKIE}, data={"organization_id": ""}
        ) as resp:
            resp.raise_for_status()
            data = await resp.json()
            return data["jwt"]

    async def query(
        self, query: str, variables: Dict[str, Any] = None
    ) -> Dict[str, Any]:
        """Executes a GraphQL query."""
        jwt = await self.get_jwt()
        headers = {"Authorization": f"Bearer {jwt}", "Content-Type": "application/json"}
        async with self.session.post(
            RUNPOD_GRAPHQL_URL,
            json={"query": query, "variables": variables},
            headers=headers,
        ) as resp:
            resp.raise_for_status()
            return await resp.json()

    async def get_logs(self, pod_id: str) -> Dict[str, Any]:
        """Fetches system and container logs."""
        jwt = await self.get_jwt()
        url = f"{RUNPOD_HAPI_HOST}/{pod_id}/logs"
        async with self.session.get(
            url, headers={"Authorization": f"Bearer {jwt}"}
        ) as resp:
            return await resp.json() if resp.status == 200 else {}

    async def get_nvidia_gpus(self) -> List[Dict[str, Any]]:
        """Lists available Nvidia GPUs on Secure Cloud."""
        query = """
        query GpuListGpuTypes {
          gpuTypes {
            id
            maxGpuCountSecureCloud
            minPodGpuCount
            displayName
            manufacturer
            securePrice
            secureCloud
          }
        }
        """
        res = await self.query(query)
        return [
            gpu
            for gpu in res["data"]["gpuTypes"]
            if gpu["manufacturer"].lower() == "nvidia" and gpu["secureCloud"] is True
        ]
