// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { console } from "forge-std/Script.sol";
import { IAccessControl } from "@openzeppelin/contracts/access/IAccessControl.sol";
import { AuthRegistry } from "auth-registry/src/AuthRegistry.sol";
import { DeployAll } from "./DeployAll.s.sol";

/**
 * @title DeployDemo
 * @notice No-arg, one-shot deployment for the Besu interbank demo. Wraps
 *         {DeployAll} with a fixed configuration where **Bank A is the issuer**:
 *         it deploys the chain, keeps DEFAULT_ADMIN + minter authority, and is
 *         the AuthRegistry policy admin.
 *
 *         Bank A = deployer (Hardhat #0). All admin/minter roles stay with Bank A
 *         (handover targets == deployer ⇒ no renounce), so the deposit-svc mimic
 *         can mint and authorize counterparties afterward.
 *
 *         Baseline infra whitelisting (address(0), the token/handler/authority
 *         contracts, Bank A) is done here so mint/burn mechanics clear the
 *         WHITELIST transfer policy. Counterparty wallets (Bank B, merchant) are
 *         authorized later through the deposit-svc `authorize` admin op — mirroring
 *         real onboarding.
 *
 *         Writes deployments/besu.json for the TypeScript services to consume.
 */
contract DeployDemo is DeployAll {
    // DEMO-ONLY well-known Hardhat accounts.
    address constant BANK_A = 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266; // issuer / deployer / minter
    address constant BANK_B = 0x70997970C51812dc3A010C7d01b50e0d17dc79C8; // holder wallet

    uint256 constant BIG = 999_999_999_999_999; // 6-decimal supply/allowance ceiling

    // Named `deploy` (not `run`) to avoid an ABI clash with the inherited
    // DeployAll.run(TokenConfig,TokenConfig,HandoverConfig) overload. Invoke with
    //   forge script scripts/DeployDemo.s.sol:DeployDemo --sig "deploy()" ...
    function deploy() external {
        require(msg.sender == BANK_A, "deployer must be Bank A (Hardhat #0)");

        TokenConfig memory rl =
            TokenConfig({ name: "Reserve Ledger Dollar", symbol: "RD", decimals: 6, policyAdmin: BANK_A, saltNonce: 0 });
        TokenConfig memory sc = TokenConfig({
            name: "USD Deposit Token",
            symbol: "WFUSD",
            decimals: 6,
            policyAdmin: BANK_A,
            saltNonce: 2
        });
        HandoverConfig memory h = HandoverConfig({
            txnMintLimit: BIG,
            minterAddress: BANK_A, // Bank A calls TokenAuthority.mint
            minterAllowance: BIG,
            rlMaxSupply: BIG,
            stablecoinMaxSupply: BIG,
            pauserAddress: BANK_A,
            unpauserAddress: BANK_A,
            blockedAddressBurnerAddress: BANK_A,
            rlAdmin: BANK_A, // == deployer ⇒ Bank A keeps admin
            stablecoinAdmin: BANK_A,
            tokenAuthorityAdmin: BANK_A
        });

        vm.startBroadcast();
        DeployResult memory r = _execute(msg.sender, rl, sc, h);

        // Baseline infra whitelisting so ERC-20 mint/burn/transfer mechanics
        // clear the WHITELIST transfer policy. Bank A is the policy admin.
        AuthRegistry reg = AuthRegistry(r.authRegistry);
        address[5] memory infra =
            [address(0), r.stablecoin, r.tokenHandler, r.tokenAuthority, BANK_A];
        for (uint256 i = 0; i < infra.length; i++) {
            reg.modifyPolicyWhitelist(r.transferPolicyId, infra[i], true);
        }
        // Handler must be a valid RL mint recipient (wrapped-handler mints RL).
        reg.modifyPolicyWhitelist(r.rlMintPolicyId, r.tokenHandler, true);
        vm.stopBroadcast();

        _writeDeploymentJson(r);

        console.log("===== Besu Interbank Demo deployed =====");
        console.log("AuthRegistry:   ", r.authRegistry);
        console.log("ReserveLedger:  ", r.reserveLedger);
        console.log("TokenAuthority: ", r.tokenAuthority);
        console.log("TokenHandler:   ", r.tokenHandler);
        console.log("Deposit Token:  ", r.stablecoin);
    }

    function _writeDeploymentJson(DeployResult memory r) internal {
        string memory o = "deployment";
        vm.serializeAddress(o, "authRegistry", r.authRegistry);
        vm.serializeAddress(o, "reserveLedger", r.reserveLedger);
        vm.serializeAddress(o, "tokenAuthority", r.tokenAuthority);
        vm.serializeAddress(o, "tokenHandler", r.tokenHandler);
        vm.serializeAddress(o, "depositToken", r.stablecoin);
        vm.serializeUint(o, "transferPolicyId", r.transferPolicyId);
        vm.serializeUint(o, "rlMintPolicyId", r.rlMintPolicyId);
        vm.serializeUint(o, "chainId", block.chainid);
        string memory json = vm.serializeUint(o, "scMintPolicyId", r.scMintPolicyId);
        vm.writeFile("deployments/besu.json", json);
        console.log("Wrote deployments/besu.json");
    }
}
