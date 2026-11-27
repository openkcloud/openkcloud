import { nodes } from "@/mocks/fixtures";
import View from "./view";

export function generateStaticParams() {
  return nodes.map((n) => ({ id: n.id }));
}

export default function Page() {
  return <View />;
}
