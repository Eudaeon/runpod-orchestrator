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
            if resp.status == 404:
                raise Exception("Session error: check RUNPOD_SESSION_ID")

            if resp.status == 401:
                raise Exception("Authentication error: check RUNPOD_CLERK_COOKIE")

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

    async def get_pod_template_args(self, template_id: str) -> str:
        """Fetches the dockerArgs for a specific template."""
        query = (
            "query getPodTemplate($id: String!) { podTemplate(id: $id) { dockerArgs } }"
        )
        res = await self.query(query, {"id": template_id})
        return res["data"]["podTemplate"].get("dockerArgs", "")

    async def get_balance(self) -> float:
        """Retrieves the current user balance."""
        res = await self.query("{ myself { clientBalance } }")
        return float(res["data"]["myself"]["clientBalance"])

    async def deploy_pod(self, input_data: Dict[str, Any], is_gpu: bool) -> str:
        """Deploys a pod and handles availability errors."""
        if is_gpu:
            q = "mutation Mutation($input: PodFindAndDeployOnDemandInput) { podFindAndDeployOnDemand(input: $input) { id } }"
            key = "podFindAndDeployOnDemand"
        else:
            q = "mutation Mutation($input: deployCpuPodInput!) { deployCpuPod(input: $input) { id } }"
            key = "deployCpuPod"

        res = await self.query(q, {"input": input_data})

        if "errors" in res:
            for error in res["errors"]:
                if "no longer any instances available" in error.get("message", ""):
                    raise Exception("No instances available")

        if not res.get("data") or not res["data"].get(key):
            raise Exception(f"Failed to create pod: {res.get('errors')}")

        return res["data"][key]["id"]

    async def get_pod_details(self, pod_id: str) -> Dict[str, Any]:
        """Fetches detailed metadata for a specific pod."""
        query = """
        query podDetailedInspector($input: PodFilter) {
          pod(input: $input) {
            costPerHr
            machine { 
              dataCenterId 
              cpuType { displayName } 
              gpuType { displayName } 
            }
          }
        }
        """
        res = await self.query(query, {"input": {"podId": pod_id}})
        return res["data"]["pod"]

    async def terminate_pod(self, pod_id: str):
        """Terminates a running pod."""
        query = "mutation terminate($input: PodTerminateInput!) { podTerminate(input: $input) }"
        await self.query(query, {"input": {"podId": pod_id}})
