import { workloads } from "@/mocks/fixtures";
import View from "./view";

export function generateStaticParams() {
  return workloads.map((w) => ({ id: w.id }));
}

export default function Page() {
  return <View />;
}
