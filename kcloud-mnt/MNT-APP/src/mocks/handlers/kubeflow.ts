import { http, HttpResponse, delay } from "msw";
import {
  notebooks,
  pipelines,
  experiments,
  inferenceServices,
} from "../fixtures";

export const kubeflowHandlers = [
  http.get("/api/kubeflow/notebooks", async () => {
    await delay(110);
    return HttpResponse.json({ items: notebooks });
  }),
  http.get("/api/kubeflow/pipelines", async () => {
    await delay(100);
    return HttpResponse.json({ items: pipelines });
  }),
  http.get("/api/kubeflow/experiments", async () => {
    await delay(100);
    return HttpResponse.json({ items: experiments });
  }),
  http.get("/api/kubeflow/serving", async () => {
    await delay(110);
    return HttpResponse.json({ items: inferenceServices });
  }),
];
